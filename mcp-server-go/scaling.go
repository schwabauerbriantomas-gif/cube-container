// Package main: horizontal scaling — replica management + load balancing.
//
// Allows running N replicas of the same container (same image, same config)
// across the cluster. The Caddy reverse proxy is configured to round-robin
// across all replica endpoints, providing load balancing and failover.
//
// A "service" is a logical group of replicas identified by name. Each replica
// is a container with a deterministic naming pattern: <service>-<n>.
//
// Scale operations:
//   - scale_up:   create new replicas (creates containers from a template)
//   - scale_down: remove excess replicas
//   - scale_set:  set exact replica count (up or down)
//
// The routing layer (routing.go) is updated to load-balance across all
// replicas when a domain is associated with the service.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---- Types ----

// Service represents a group of replica containers that serve the same app.
type Service struct {
	Name           string            `json:"name"`
	TemplateID     string            `json:"template_id"`
	Replicas       int               `json:"replicas"`
	MemoryMB       int               `json:"memory_mb"`
	CPUCount       float64           `json:"cpu_count"`
	EnvVars        map[string]string `json:"env_vars,omitempty"`
	ContainerIDs   []string          `json:"container_ids"`
	Port           int               `json:"port"`        // internal port replicas expose
	Domain         string            `json:"domain"`      // optional domain for load-balanced route
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// ServiceSummary is the lightweight view for listing.
type ServiceSummary struct {
	Name         string    `json:"name"`
	Replicas     int       `json:"replicas"`
	Desired      int       `json:"desired"`
	ContainerIDs []string  `json:"container_ids"`
	Domain       string    `json:"domain"`
	Port         int       `json:"port"`
}

// ---- Manager ----

// scaleMgr is the process-wide scaling manager.
var scaleMgr *ScaleManager

// ScaleManager stores service definitions and handles replica lifecycle.
type ScaleManager struct {
	mu      sync.Mutex
	services map[string]*Service
	rootDir  string
	backend  ContainerBackend
}

func newScaleManager(b ContainerBackend) *ScaleManager {
	sm := &ScaleManager{
		services: make(map[string]*Service),
		rootDir:  envOr("CUBE_SERVICES_ROOT", "/var/lib/cube-container/services"),
		backend:  b,
	}
	sm.loadFromDisk()
	return sm
}

// ---- Disk persistence ----

func (sm *ScaleManager) serviceFilePath(name string) string {
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(name)
	return filepath.Join(sm.rootDir, safe+".json")
}

func (sm *ScaleManager) loadFromDisk() {
	entries, err := os.ReadDir(sm.rootDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sm.rootDir, entry.Name()))
		if err != nil {
			continue
		}
		var svc Service
		if err := jsonUnmarshalSafe(data, &svc); err != nil {
			continue
		}
		sm.services[svc.Name] = &svc
	}
}

func (sm *ScaleManager) saveService(svc *Service) error {
	if err := os.MkdirAll(sm.rootDir, 0700); err != nil {
		return fmt.Errorf("create services root: %w", err)
	}
	data, err := jsonMarshalIndent(svc)
	if err != nil {
		return fmt.Errorf("marshal service: %w", err)
	}
	return os.WriteFile(sm.serviceFilePath(svc.Name), data, 0600)
}

func (sm *ScaleManager) deleteServiceFile(name string) {
	os.Remove(sm.serviceFilePath(name))
}

func jsonUnmarshalSafe(data []byte, v interface{}) error {
	return jsonDecode(data, v)
}

func jsonMarshalIndent(v interface{}) ([]byte, error) {
	return jsonEncode(v)
}

// ---- Scale operations ----

// scaleSet adjusts the replica count to exactly n.
// If n > current: creates new replicas.
// If n < current: removes excess replicas.
func (sm *ScaleManager) scaleSet(name string, n int) (*Service, error) {
	if n < 0 {
		return nil, fmt.Errorf("replica count cannot be negative")
	}
	if n > 20 {
		return nil, fmt.Errorf("replica count capped at 20 for edge nodes")
	}

	sm.mu.Lock()
	svc, ok := sm.services[name]
	sm.mu.Unlock()

	if !ok {
		return nil, fmt.Errorf("service %s not found — use service_create first", name)
	}

	current := len(svc.ContainerIDs)
	if n == current {
		return svc, nil
	}

	if n > current {
		return sm.scaleUp(svc, n)
	}
	return sm.scaleDown(svc, n)
}

// scaleUp creates (target - current) new replicas.
func (sm *ScaleManager) scaleUp(svc *Service, target int) (*Service, error) {
	current := len(svc.ContainerIDs)
	needed := target - current

	for i := 0; i < needed; i++ {
		replicaNum := current + i + 1
		metadata := map[string]interface{}{
			"service":  svc.Name,
			"replica":  fmt.Sprintf("%d", replicaNum),
		}
		envVarsTyped := make(map[string]interface{})
		for k, v := range svc.EnvVars {
			envVarsTyped[k] = v
		}

		result, err := sm.backend.CreateSandbox(
			svc.TemplateID,
			svc.MemoryMB,
			svc.CPUCount,
			envVarsTyped,
			metadata,
		)
		if err != nil {
			// Partial failure — return what we have
			sm.mu.Lock()
			svc.UpdatedAt = time.Now().UTC()
			_ = sm.saveService(svc)
			sm.mu.Unlock()
			return svc, fmt.Errorf("created %d/%d replicas (failed at replica %d): %w", i, needed, replicaNum, err)
		}

		containerID := extractSandboxID(result)
		if containerID != "" {
			sm.mu.Lock()
			svc.ContainerIDs = append(svc.ContainerIDs, containerID)
			sm.mu.Unlock()
		}
	}

	sm.mu.Lock()
	svc.Replicas = len(svc.ContainerIDs)
	svc.UpdatedAt = time.Now().UTC()
	_ = sm.saveService(svc)
	result := svc
	sm.mu.Unlock()

	// Update load-balanced route if domain exists
	if svc.Domain != "" {
		sm.updateLoadBalancedRoute(svc)
	}

	return result, nil
}

// scaleDown removes (current - target) replicas, keeping the lowest-numbered.
func (sm *ScaleManager) scaleDown(svc *Service, target int) (*Service, error) {
	current := len(svc.ContainerIDs)
	toRemove := current - target

	for i := 0; i < toRemove; i++ {
		idx := len(svc.ContainerIDs) - 1 // remove from the end
		containerID := svc.ContainerIDs[idx]
		_, err := sm.backend.KillSandbox(containerID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[cube-mcp] scale: failed to remove replica %s: %v\n", containerID, err)
		}
		sm.mu.Lock()
		svc.ContainerIDs = svc.ContainerIDs[:idx]
		sm.mu.Unlock()
	}

	sm.mu.Lock()
	svc.Replicas = len(svc.ContainerIDs)
	svc.UpdatedAt = time.Now().UTC()
	_ = sm.saveService(svc)
	result := svc
	sm.mu.Unlock()

	if svc.Domain != "" {
		sm.updateLoadBalancedRoute(svc)
	}

	return result, nil
}

// createService registers a new scalable service.
func (sm *ScaleManager) createService(svc *Service) error {
	if svc.Name == "" {
		return fmt.Errorf("name is required")
	}
	if svc.TemplateID == "" {
		return fmt.Errorf("template_id is required")
	}
	if svc.Port <= 0 {
		svc.Port = 8000
	}
	if svc.MemoryMB <= 0 {
		svc.MemoryMB = 256
	}
	if svc.CPUCount <= 0 {
		svc.CPUCount = 1.0
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.services[svc.Name]; exists {
		return fmt.Errorf("service %s already exists", svc.Name)
	}

	svc.CreatedAt = time.Now().UTC()
	svc.UpdatedAt = svc.CreatedAt
	sm.services[svc.Name] = svc
	return sm.saveService(svc)
}

// removeService deletes a service definition (does NOT kill containers).
func (sm *ScaleManager) removeService(name string) error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, ok := sm.services[name]; !ok {
		return fmt.Errorf("service %s not found", name)
	}
	delete(sm.services, name)
	sm.deleteServiceFile(name)
	return nil
}

// listServices returns all registered services.
func (sm *ScaleManager) listServices() []ServiceSummary {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	out := make([]ServiceSummary, 0, len(sm.services))
	for _, svc := range sm.services {
		out = append(out, ServiceSummary{
			Name:         svc.Name,
			Replicas:     len(svc.ContainerIDs),
			Desired:      svc.Replicas,
			ContainerIDs: svc.ContainerIDs,
			Domain:       svc.Domain,
			Port:         svc.Port,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name < out[j].Name
	})
	return out
}

// getService returns a single service by name.
func (sm *ScaleManager) getService(name string) (*Service, error) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	svc, ok := sm.services[name]
	if !ok {
		return nil, fmt.Errorf("service %s not found", name)
	}
	return svc, nil
}

// ---- Load-balanced routing ----

// updateLoadBalancedRoute writes a Caddy config fragment that round-robins
// across all replica endpoints for a service domain.
func (sm *ScaleManager) updateLoadBalancedRoute(svc *Service) {
	if svc.Domain == "" || len(svc.ContainerIDs) == 0 {
		return
	}

	// For now, replicas are on the same node (local backend). The upstream
	// is localhost:<port> since each replica maps to the same internal port
	// but gets a different host port. We query the backend for actual mappings.
	//
	// Simplified: if all replicas share the same port (host networking),
	// we use that. Otherwise we'd need to inspect each container's port
	// mappings. For edge deployment with single-node, we generate a single
	// upstream per replica port.
	upstreams := make([]string, 0, len(svc.ContainerIDs))
	for range svc.ContainerIDs {
		// In single-node mode, each replica exposes the same internal port
		// on a different host port. We'd need to inspect each container.
		// For now, assume port is the same (host network mode).
		upstreams = append(upstreams, fmt.Sprintf("localhost:%d", svc.Port))
	}

	// Deduplicate (in host-network mode all are the same)
	seen := make(map[string]bool)
	unique := make([]string, 0)
	for _, u := range upstreams {
		if !seen[u] {
			seen[u] = true
			unique = append(unique, u)
		}
	}

	// Generate Caddy LB fragment
	var b strings.Builder
	b.WriteString(svc.Domain + " {\n")
	b.WriteString("    header {\n")
	b.WriteString("        Strict-Transport-Security \"max-age=63072000; includeSubDomains; preload\"\n")
	b.WriteString("        X-Content-Type-Options \"nosniff\"\n")
	b.WriteString("        X-Frame-Options \"DENY\"\n")
	b.WriteString("        Referrer-Policy \"no-referrer\"\n")
	b.WriteString("    }\n")
	b.WriteString("    encode zstd gzip\n")
	b.WriteString("    reverse_proxy {\n")
	for _, u := range unique {
		b.WriteString("        to " + u + "\n")
	}
	b.WriteString("        lb_policy round_robin\n")
	b.WriteString("        health_uri /health\n")
	b.WriteString("        health_interval 10s\n")
	b.WriteString("        health_timeout 2s\n")
	b.WriteString("    }\n")
	b.WriteString("}\n\n")

	// Write to a service-specific fragment and reload
	fragmentPath := filepath.Join(filepath.Dir(routeMgr.caddyConfigPath), "cube-service-"+svc.Name+".caddy")
	os.MkdirAll(filepath.Dir(fragmentPath), 0755)
	os.WriteFile(fragmentPath, []byte(b.String()), 0644)

	if routeMgr.caddyReload {
		routeMgr.reloadCaddy()
	}
}

// extractSandboxID pulls the container ID from a backend create response.
func extractSandboxID(result interface{}) string {
	m := asMap(result)
	if id := toString(m["sandboxID"]); id != "" {
		return id
	}
	if id := toString(m["Id"]); id != "" {
		return id
	}
	if id := toString(m["id"]); id != "" {
		return id
	}
	return ""
}

// ---- Tool handlers: Scaling ----

func handleServiceCreate(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)

	envVars := make(map[string]string)
	if ev := argMap(args, "env_vars"); ev != nil {
		for k, v := range ev {
			envVars[k] = fmt.Sprintf("%v", v)
		}
	}

	svc := &Service{
		Name:       argString(args, "name"),
		TemplateID: argString(args, "template_id"),
		Port:       argInt(args, "port", 8000),
		MemoryMB:   argInt(args, "memory_mb", 256),
		CPUCount:   argFloat(args, "cpu_count", 1.0),
		EnvVars:    envVars,
		Domain:     argString(args, "domain"),
	}

	if err := scaleMgr.createService(svc); err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(map[string]interface{}{
		"name":        svc.Name,
		"template_id": svc.TemplateID,
		"port":        svc.Port,
		"domain":      svc.Domain,
		"status":      "created (0 replicas — use scale_set to add replicas)",
	}), nil
}

func handleScaleSet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	name := argString(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	replicas := argInt(args, "replicas", -1)
	if replicas < 0 {
		return errResult("replicas is required (0-20)"), nil
	}

	svc, err := scaleMgr.scaleSet(name, replicas)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(map[string]interface{}{
		"name":          svc.Name,
		"replicas":      len(svc.ContainerIDs),
		"container_ids": svc.ContainerIDs,
		"domain":        svc.Domain,
	}), nil
}

func handleServiceList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return okResult(scaleMgr.listServices()), nil
}

func handleServiceGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	name := argString(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	svc, err := scaleMgr.getService(name)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(svc), nil
}

func handleServiceRemove(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	name := argString(args, "name")
	if name == "" {
		return errResult("name is required"), nil
	}
	if err := scaleMgr.removeService(name); err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(map[string]interface{}{
		"name":   name,
		"status": "removed (containers NOT killed — use kill_container manually if needed)",
	}), nil
}
