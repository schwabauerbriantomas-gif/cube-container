// Package main: concurrency stress tests (deliverable D8).
//
// These tests run under `go test -race` to verify that the concurrent
// access patterns in auth.go (KeyStore, rateLimiter, AuditLogger),
// backup.go (BackupManager), and the RBAC permission map are free of
// data races. They exercise the same primitives the production server
// hits from many goroutines at once.
//
// Each test coordinates goroutines with sync.WaitGroup and uses
// t.TempDir() for any file-based state. Every test is designed to
// complete well under 10 seconds.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrent_RateLimiter: 1,000 goroutines hit Allow() across 100
// distinct keys, each goroutine calling Allow() 10 times. Under the race
// detector this proves the rateLimiter's mutex + map access is safe.
func TestConcurrent_RateLimiter(t *testing.T) {
	// High limit so Allow() doesn't reject under the burst; we are
	// testing for data-race safety, not rate-limit correctness.
	rl := newRateLimiter(1_000_000, time.Hour)

	const goroutines = 1000
	const keys = 100
	const callsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	var allowed int64
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("conc-key-%d", id%keys)
			<-start // release all goroutines at once
			for j := 0; j < callsPerGoroutine; j++ {
				if rl.Allow(key) {
					atomic.AddInt64(&allowed, 1)
				}
			}
		}(i)
	}

	close(start) // fire the thundering herd
	wg.Wait()

	total := int64(goroutines * callsPerGoroutine)
	if allowed != total {
		t.Fatalf("expected all %d calls allowed, got %d", total, allowed)
	}
}

// TestConcurrent_KeyStoreValidate: 100 goroutines generate keys while
// 100 goroutines concurrently validate keys. Proves the KeyStore's
// RWMutex protects the map under mixed read/write load.
func TestConcurrent_KeyStoreValidate(t *testing.T) {
	dir := t.TempDir()
	ks := &KeyStore{
		keys:     make(map[string]*APIKey),
		filePath: filepath.Join(dir, "keys.json"),
	}

	// Seed a few keys so validators have something to find.
	seed := make([]*APIKey, 0, 10)
	for i := 0; i < 10; i++ {
		k, err := ks.GenerateKey(RoleOperator, fmt.Sprintf("seed-%d", i))
		if err != nil {
			t.Fatalf("seed key gen: %v", err)
		}
		seed = append(seed, k)
	}

	const workers = 100
	var wg sync.WaitGroup
	wg.Add(workers * 2)

	var genErr atomic.Value
	var valErr atomic.Value
	start := make(chan struct{})

	// Generators: create new keys concurrently.
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			<-start
			_, err := ks.GenerateKey(RoleViewer, fmt.Sprintf("gen-%d", id))
			if err != nil {
				genErr.Store(err)
			}
		}(i)
	}

	// Validators: validate random seeded keys concurrently.
	for i := 0; i < workers; i++ {
		go func(id int) {
			defer wg.Done()
			<-start
			k := seed[id%len(seed)]
			// Validate against the real secret; we don't assert success
			// (the map may be mutating), we only assert no panic/race.
			_, _ = ks.Validate(k.Key, k.Secret)
			// Also try a bogus key to exercise the not-found path.
			_, _ = ks.Validate("cc_live_does_not_exist", "bogus")
		}(i)
	}

	close(start)
	wg.Wait()

	if v := genErr.Load(); v != nil {
		t.Fatalf("generator error: %v", v)
	}
	if v := valErr.Load(); v != nil {
		t.Fatalf("validator error: %v", v)
	}
}

// TestConcurrent_AuditLogger: 500 goroutines each log 10 entries to the
// SAME AuditLogger simultaneously, then the tamper-evident hash chain
// MUST verify intact.
func TestConcurrent_AuditLogger(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.logl")
	al := &AuditLogger{file: mustOpenFile(path)}

	const goroutines = 500
	const entriesEach = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			<-start
			for j := 0; j < entriesEach; j++ {
				al.Log(AuditEntry{
					Timestamp:  time.Now().UTC().Format(time.RFC3339Nano),
					Key:        fmt.Sprintf("cc_live_g%d***", id),
					Role:       "operator",
					Method:     "POST",
					Path:       "/mcp",
					StatusCode: 200,
					Duration:   "1ms",
					Allowed:    true,
				})
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// Flush to disk before verifying.
	if err := al.file.Sync(); err != nil {
		t.Fatalf("sync audit log: %v", err)
	}

	// The chain integrity is the real assertion: concurrent appends must
	// not corrupt the prev-hash linkage.
	count, err := VerifyAuditChain(path)
	if err != nil {
		t.Fatalf("audit chain broken under concurrency: %v", err)
	}
	expected := goroutines * entriesEach
	if count != expected {
		t.Fatalf("expected %d audit entries, got %d", expected, count)
	}
}

// TestConcurrent_BackupVolume: 10 goroutines each create their own
// volume and back it up concurrently. Each backup is then restored and
// its integrity verified — no file corruption under parallel I/O.
func TestConcurrent_BackupVolume(t *testing.T) {
	dir := t.TempDir()
	dm := &DeployManager{VolumesRoot: filepath.Join(dir, "volumes")}
	if err := os.MkdirAll(dm.VolumesRoot, 0755); err != nil {
		t.Fatal(err)
	}
	bm := &BackupManager{
		BackupRoot:  filepath.Join(dir, "backups"),
		VolumesRoot: dm.VolumesRoot,
		DeployMgr:   dm,
	}

	const goroutines = 10
	type result struct {
		id      string
		path    string
		sha     string
		volName string
		content string
	}
	results := make([]result, goroutines)
	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			<-start

			volName := fmt.Sprintf("vol-%d", id)
			content := fmt.Sprintf("payload-for-%d", id)

			// Each goroutine owns a distinct volume → no cross-goroutine
			// contention on the same source directory.
			if _, err := dm.CreateVolume(volName); err != nil {
				t.Errorf("create volume %s: %v", volName, err)
				return
			}
			volPath := filepath.Join(dm.VolumesRoot, volName)
			if err := os.WriteFile(filepath.Join(volPath, "data.txt"), []byte(content), 0644); err != nil {
				t.Errorf("write %s: %v", volName, err)
				return
			}

			bk, err := bm.BackupVolume(volName)
			if err != nil {
				t.Errorf("backup %s: %v", volName, err)
				return
			}
			results[id] = result{
				id:      bk.ID,
				path:    bk.Path,
				sha:     bk.SHA256,
				volName: volName,
				content: content,
			}
		}(i)
	}

	close(start)
	wg.Wait()

	// Verify every backup: integrity checksum matches and restore works.
	for i, r := range results {
		if r.id == "" {
			t.Errorf("goroutine %d produced no backup", i)
			continue
		}
		got, err := computeFileChecksum(r.path)
		if err != nil {
			t.Errorf("checksum %s: %v", r.id, err)
			continue
		}
		if got != r.sha {
			t.Errorf("backup %s corrupted: checksum mismatch (manifest %s vs file %s)", r.id, r.sha, got)
			continue
		}

		// Restore into a fresh location and confirm content round-trips.
		rr, err := bm.RestoreBackup(r.id)
		if err != nil {
			t.Errorf("restore %s: %v", r.id, err)
			continue
		}
		if !rr.IntegrityOK {
			t.Errorf("restore %s reported integrity failure", r.id)
			continue
		}
		data, err := os.ReadFile(filepath.Join(dm.VolumesRoot, r.volName, "data.txt"))
		if err != nil {
			t.Errorf("read restored %s: %v", r.id, err)
			continue
		}
		if string(data) != r.content {
			t.Errorf("restore %s content mismatch: got %q want %q", r.id, string(data), r.content)
		}
	}
}

// TestConcurrent_AuthMiddlewareHTTP: stand up an httptest server wrapped
// in the auth middleware, then fire 100 concurrent requests from distinct
// keys. No panics, and every response must carry a valid HTTP status code.
func TestConcurrent_AuthMiddlewareHTTP(t *testing.T) {
	dir := t.TempDir()
	ks := &KeyStore{
		keys:     make(map[string]*APIKey),
		filePath: filepath.Join(dir, "keys.json"),
	}
	// High limit so concurrent requests aren't rejected by rate limiting.
	rl := newRateLimiter(1_000_000, time.Hour)
	al := &AuditLogger{file: mustOpenFile(filepath.Join(dir, "audit.logl"))}
	am := newAuthMiddleware(ks, rl, al)

	// Issue 100 keys so each request uses a distinct key.
	keys := make([]*APIKey, 100)
	for i := 0; i < 100; i++ {
		k, err := ks.GenerateKey(RoleOperator, fmt.Sprintf("http-%d", i))
		if err != nil {
			t.Fatalf("gen key %d: %v", i, err)
		}
		keys[i] = k
	}

	// Inner handler: always 200 OK.
	handler := am.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"ok":true}`))
	}), func(*http.Request) string { return "" })

	server := httptest.NewServer(handler)
	defer server.Close()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var failCount int64
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			<-start

			k := keys[id]
			req, err := http.NewRequest("POST", server.URL+"/mcp", nil)
			if err != nil {
				t.Errorf("new request %d: %v", id, err)
				atomic.AddInt64(&failCount, 1)
				return
			}
			req.Header.Set("X-API-Key", k.Key)
			req.Header.Set("X-API-Secret", k.Secret)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Errorf("do request %d: %v", id, err)
				atomic.AddInt64(&failCount, 1)
				return
			}
			resp.Body.Close()

			// Any standard HTTP status is acceptable; we are checking
			// for absence of panics/hangs, not authorization outcomes.
			if resp.StatusCode < 100 || resp.StatusCode >= 600 {
				t.Errorf("invalid status %d for request %d", resp.StatusCode, id)
				atomic.AddInt64(&failCount, 1)
			}
		}(i)
	}

	close(start)
	wg.Wait()

	if failCount > 0 {
		t.Fatalf("%d requests failed", failCount)
	}
}

// TestConcurrent_RBACCheck: 10,000 goroutines call canExecute() with
// random roles and tools. Pure read concurrency over the shared
// toolPermissions map — the race detector confirms read-only access is
// safe.
func TestConcurrent_RBACCheck(t *testing.T) {
	roles := []Role{RoleViewer, RoleOperator, RoleAdmin}
	tools := []string{
		"list_containers",
		"create_container",
		"delete_volume",
		"deploy_from_git",
		"cluster_health",
		"restore_backup",
		"create_route",
		"list_routes",
		"nonexistent_tool", // exercises the not-found branch
	}

	const goroutines = 10_000
	var wg sync.WaitGroup
	wg.Add(goroutines)
	var checked int64
	start := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			<-start
			role := roles[id%len(roles)]
			tool := tools[id%len(tools)]
			_ = canExecute(role, tool) // result intentionally unused
			atomic.AddInt64(&checked, 1)
		}(i)
	}

	close(start)
	wg.Wait()

	if checked != goroutines {
		t.Fatalf("expected %d checks, got %d", goroutines, checked)
	}
}
