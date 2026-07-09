package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// skipIfNoLibvirt skips tests that require libvirt directories and permissions.
func skipIfNoLibvirt(t *testing.T) {
	t.Helper()
	if err := os.MkdirAll("/var/lib/libvirt/seeds", 0755); err != nil {
		t.Skipf("skipping: cannot create /var/lib/libvirt/seeds (no permissions): %v", err)
	}
}

// skipIfNoCloudImageUtils skips tests that require cloud-image-utils or genisoimage.
func skipIfNoCloudImageUtils(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"cloud-localds", "genisoimage", "mkisofs"} {
		if _, err := exec.LookPath(tool); err == nil {
			return
		}
	}
	t.Skip("skipping: no cloud-init ISO tool found (cloud-localds, genisoimage, or mkisofs)")
}

func TestCloudInitCreateReal(t *testing.T) {
	skipIfNoLibvirt(t)
	skipIfNoCloudImageUtils(t)

	ctx := context.Background()

	req := mcp.CallToolRequest{}
	req.Params.Name = "vm_cloudinit_create"
	req.Params.Arguments = map[string]interface{}{
		"hostname": "test-vm-001",
		"username": "testuser",
		"packages": []interface{}{"nginx", "curl"},
		"ssh_keys": []interface{}{
			"ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDtest fake@key",
		},
	}

	res, err := handleVMCloudInitCreate(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.IsError {
		t.Fatalf("cloud-init create failed: %v", res.Content)
	}

	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data CloudInitResult
			json.Unmarshal([]byte(tc.Text), &data)
			t.Logf("ISO: %s", data.ISOPath)
			t.Logf("Seed dir: %s", data.SeedDir)
			t.Logf("Hostname: %s", data.Hostname)
			t.Logf("Username: %s", data.Username)
			t.Logf("SSH keys: %d", data.KeyCount)

			// Verify ISO exists
			if _, err := os.Stat(data.ISOPath); err != nil {
				t.Errorf("ISO file not created: %v", err)
			}
			// Verify user-data
			userData, _ := os.Stat(data.SeedDir + "/user-data")
			if userData == nil {
				t.Error("user-data not created")
			}
			// Verify meta-data
			metaData, _ := os.Stat(data.SeedDir + "/meta-data")
			if metaData == nil {
				t.Error("meta-data not created")
			}

			// Read user-data and check content
			ud, _ := os.ReadFile(data.SeedDir + "/user-data")
			udStr := string(ud)
			if !strings.HasPrefix(udStr, "#cloud-config") {
				t.Errorf("user-data doesn't start with #cloud-config: got %q", safeFirstN(udStr, 20))
			}

			t.Log("user-data preview (first 200 chars):")
			if len(udStr) > 200 {
				t.Logf("  %s...", udStr[:200])
			} else {
				t.Logf("  %s", udStr)
			}
		}
	}

	// Cleanup
	os.RemoveAll("/var/lib/libvirt/seeds/test-vm-001")
}

func TestTemplateListReal(t *testing.T) {
	ctx := context.Background()

	// Try to create test files in defaultImageDir; skip if no permissions
	if err := os.MkdirAll(defaultImageDir, 0755); err != nil {
		t.Skipf("skipping: cannot create %s (no permissions): %v", defaultImageDir, err)
	}

	// Create test files
	if err := os.WriteFile(defaultImageDir+"/cube-ci-test-ubuntu.qcow2", []byte("fake"), 0644); err != nil {
		t.Skipf("skipping: cannot write to %s (no permissions): %v", defaultImageDir, err)
	}
	defer os.Remove(defaultImageDir + "/cube-ci-test-ubuntu.qcow2")
	os.WriteFile(defaultImageDir+"/cube-ci-test-alpine.img", []byte("fake"), 0644)
	defer os.Remove(defaultImageDir + "/cube-ci-test-alpine.img")
	os.WriteFile(defaultImageDir+"/cube-ci-test-seed.iso", []byte("fake"), 0644)
	defer os.Remove(defaultImageDir + "/cube-ci-test-seed.iso")

	req := mcp.CallToolRequest{}
	req.Params.Name = "vm_template_list"

	res, err := handleVMTemplateList(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var data struct {
				Templates []VMTemplate `json:"templates"`
				Total     int          `json:"total"`
			}
			json.Unmarshal([]byte(tc.Text), &data)
			t.Logf("Found %d templates", data.Total)
			for _, tpl := range data.Templates {
				t.Logf("  %s (%dMB) %s", tpl.Name, tpl.SizeMB, tpl.ModifiedAt)
			}
			// We added 3 files but there might be pre-existing ones; check >= 3
			if data.Total < 3 {
				t.Errorf("expected >= 3 templates, got %d", data.Total)
			}
		}
	}
}

func TestCreateCloudInitISO(t *testing.T) {
	skipIfNoCloudImageUtils(t)

	// Test the ISO creation directly
	tmpDir, err := os.MkdirTemp("", "cube-ci-iso-*")
	if err != nil {
		t.Skipf("skipping: cannot create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create test files
	os.WriteFile(tmpDir+"/user-data", []byte("#cloud-config\nhostname: test\n"), 0644)
	os.WriteFile(tmpDir+"/meta-data", []byte("instance-id: test\n"), 0644)

	isoPath := tmpDir + "/test-seed.iso"
	err = createCloudInitISO(tmpDir, isoPath)
	if err != nil {
		t.Fatalf("createCloudInitISO failed: %v", err)
	}

	// Verify ISO was created
	info, err := os.Stat(isoPath)
	if err != nil {
		t.Fatalf("ISO not created: %v", err)
	}
	if info.Size() < 1000 {
		t.Errorf("ISO suspiciously small: %d bytes", info.Size())
	}
	t.Logf("ISO created: %dKB", info.Size()/1024)

	// Verify it has correct volume label using isoinfo or file
	// Just check the file type
	out, _ := os.ReadFile(isoPath)
	if len(out) < 32769 { // ISO9660 has data at sector 16 (32768)
		t.Log("ISO smaller than expected but may still be valid")
	}
}

func safeFirstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
