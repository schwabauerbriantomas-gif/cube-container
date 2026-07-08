// Package main: automatic TLS + domain routing via Caddy config automation.
// Caddy obtains and renews Let's Encrypt certificates natively when a domain
// name is used as the site address — no extra config is needed.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ---- Route model ----

// Route represents a reverse proxy entry: a public domain mapped to a
// container port. Caddy handles HTTPS automatically.
type Route struct {
	Domain      string    `json:"domain"`
	ContainerID string    `json:"container_id"`
	TargetPort  int       `json:"target_port"`
	TLSEnabled  bool      `json:"tls_enabled"`
	CreatedAt   time.Time `json:"created_at"`
	PathPrefix  string    `json:"path_prefix,omitempty"`
}

// ---- RouteManager ----

// RouteManager persists route entries as individual JSON files (one per domain)
// and regenerates a Caddyfile fragment that Caddy imports.
type RouteManager struct {
	mu              sync.Mutex
	routesRoot      string // CUBE_ROUTES_ROOT — directory for per-route JSON
	caddyConfigPath string // CUBE_CADDY_CONFIG_PATH — generated fragment
	caddyReload     bool   // CUBE_CADDY_RELOAD — invoke `caddy reload` after regen
}

func newRouteManager() *RouteManager {
	rm := &RouteManager{
		routesRoot:      envOr("CUBE_ROUTES_ROOT", "/var/lib/cube-container/routes"),
		caddyConfigPath: envOr("CUBE_CADDY_CONFIG_PATH", "/etc/caddy/cube-routes.caddy"),
		caddyReload:     strings.EqualFold(envOr("CUBE_CADDY_RELOAD", "false"), "true"),
	}
	// Ensure cube-routes.caddy exists so the main Caddyfile's import doesn't fail.
	os.MkdirAll(filepath.Dir(rm.caddyConfigPath), 0755)
	if _, err := os.Stat(rm.caddyConfigPath); os.IsNotExist(err) {
		os.WriteFile(rm.caddyConfigPath,
			[]byte("# Auto-created by cube-container MCP. Dynamic routes appear here.\n"), 0644)
	}
	return rm
}

// routeFilePath returns the JSON path for a given domain.
func (rm *RouteManager) routeFilePath(domain string) string {
	// Sanitize the domain into a safe filename (replace dots/dashes stay, slash removed).
	safe := strings.NewReplacer("/", "_", "\\", "_").Replace(domain)
	return filepath.Join(rm.routesRoot, safe+".json")
}

// createRoute writes a route entry to disk and regenerates the Caddy config.
func (rm *RouteManager) createRoute(domain, containerID string, targetPort int, pathPrefix string) (*Route, error) {
	if domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	// Sanitize domain to prevent Caddy config injection (H4).
	// A domain with newlines could inject arbitrary Caddy config directives.
	if err := validateDomain(domain); err != nil {
		return nil, err
	}
	if containerID == "" {
		return nil, fmt.Errorf("container_id is required")
	}
	if targetPort <= 0 || targetPort > 65535 {
		return nil, fmt.Errorf("target_port must be between 1 and 65535")
	}
	if pathPrefix != "" {
		if err := validatePathPrefix(pathPrefix); err != nil {
			return nil, err
		}
	}

	r := &Route{
		Domain:      domain,
		ContainerID: containerID,
		TargetPort:  targetPort,
		TLSEnabled:  true, // Caddy always provisions TLS for named domains
		CreatedAt:   time.Now().UTC(),
		PathPrefix:  pathPrefix,
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	if err := os.MkdirAll(rm.routesRoot, 0700); err != nil {
		return nil, fmt.Errorf("create routes root: %w", err)
	}

	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal route: %w", err)
	}
	if err := os.WriteFile(rm.routeFilePath(domain), data, 0600); err != nil {
		return nil, fmt.Errorf("write route file: %w", err)
	}

	if err := rm.generateCaddyConfigLocked(); err != nil {
		return nil, err
	}

	return r, nil
}

// deleteRoute removes a route entry and regenerates the Caddy config.
func (rm *RouteManager) deleteRoute(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain is required")
	}

	rm.mu.Lock()
	defer rm.mu.Unlock()

	path := rm.routeFilePath(domain)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("no route found for domain %s", domain)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove route file: %w", err)
	}

	if err := rm.generateCaddyConfigLocked(); err != nil {
		return err
	}

	return nil
}

// listRoutes returns all configured routes sorted by domain.
func (rm *RouteManager) listRoutes() ([]*Route, error) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	entries, err := os.ReadDir(rm.routesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Route{}, nil
		}
		return nil, fmt.Errorf("read routes root: %w", err)
	}

	var routes []*Route
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rm.routesRoot, entry.Name()))
		if err != nil {
			continue
		}
		var r Route
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		routes = append(routes, &r)
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Domain < routes[j].Domain
	})

	return routes, nil
}

// generateCaddyConfig regenerates the Caddyfile fragment from the stored routes.
// It acquires the mutex; internal callers should use generateCaddyConfigLocked.
func (rm *RouteManager) generateCaddyConfig() error {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	return rm.generateCaddyConfigLocked()
}

// generateCaddyConfigLocked writes a Caddyfile fragment importing all routes.
// Caller must hold rm.mu.
//
// Caddy automatically obtains and renews Let's Encrypt TLS certificates for
// every site address that is a domain name. The main Caddyfile should contain:
//
//	import /etc/caddy/cube-routes.caddy
//
// Each generated site block looks like:
//
//	example.com {
//	    reverse_proxy localhost:8080
//	}
//
// When a path_prefix is set, a route matcher scopes the reverse proxy:
//
//	example.com {
//	    route /api* {
//	        reverse_proxy localhost:8080
//	    }
//	}
func (rm *RouteManager) generateCaddyConfigLocked() error {
	routes, err := rm.listRoutesLocked()
	if err != nil {
		return err
	}

	var b strings.Builder
	b.WriteString("# Auto-generated by cube-container MCP — do not edit manually.\n")
	b.WriteString("# Caddy obtains Let's Encrypt TLS certificates automatically for each domain.\n")
	b.WriteString("# Regenerated at: " + time.Now().UTC().Format(time.RFC3339) + "\n\n")

	if len(routes) == 0 {
		b.WriteString("# No routes configured.\n")
	} else {
		for _, r := range routes {
			upstream := fmt.Sprintf("localhost:%d", r.TargetPort)
			b.WriteString(r.Domain + " {\n")
			// Security headers on every auto-generated route
			b.WriteString("    header {\n")
			b.WriteString("        Strict-Transport-Security \"max-age=63072000; includeSubDomains; preload\"\n")
			b.WriteString("        X-Content-Type-Options \"nosniff\"\n")
			b.WriteString("        X-Frame-Options \"DENY\"\n")
			b.WriteString("        Referrer-Policy \"no-referrer\"\n")
			b.WriteString("    }\n")
			b.WriteString("    encode zstd gzip\n")
			if r.PathPrefix != "" {
				prefix := r.PathPrefix
				if !strings.HasPrefix(prefix, "/") {
					prefix = "/" + prefix
				}
				// Caddy path matcher: append "*" if it looks like a directory prefix.
				matcher := prefix
				if !strings.HasSuffix(matcher, "*") {
					matcher = matcher + "*"
				}
				b.WriteString("    route " + matcher + " {\n")
				b.WriteString("        reverse_proxy " + upstream + "\n")
				b.WriteString("    }\n")
			} else {
				b.WriteString("    reverse_proxy " + upstream + "\n")
			}
			b.WriteString("}\n\n")
		}
	}

	if err := os.MkdirAll(filepath.Dir(rm.caddyConfigPath), 0755); err != nil {
		return fmt.Errorf("create caddy config dir: %w", err)
	}
	if err := os.WriteFile(rm.caddyConfigPath, []byte(b.String()), 0644); err != nil {
		return fmt.Errorf("write caddy config: %w", err)
	}

	if rm.caddyReload {
		rm.reloadCaddy()
	}

	return nil
}

// listRoutesLocked is the lock-free inner helper for listRoutes.
// Caller must hold rm.mu.
func (rm *RouteManager) listRoutesLocked() ([]*Route, error) {
	entries, err := os.ReadDir(rm.routesRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return []*Route{}, nil
		}
		return nil, fmt.Errorf("read routes root: %w", err)
	}

	var routes []*Route
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(rm.routesRoot, entry.Name()))
		if err != nil {
			continue
		}
		var r Route
		if err := json.Unmarshal(data, &r); err != nil {
			continue
		}
		routes = append(routes, &r)
	}

	sort.Slice(routes, func(i, j int) bool {
		return routes[i].Domain < routes[j].Domain
	})

	return routes, nil
}

// reloadCaddy triggers a graceful Caddy config reload. Errors are logged to
// stderr but do not fail the calling operation — the config file is still valid.
func (rm *RouteManager) reloadCaddy() {
	// Reload the MAIN Caddyfile (which imports cube-routes.caddy), not the fragment.
	mainConfig := envOr("CUBE_CADDY_MAIN_CONFIG", "/etc/caddy/Caddyfile")
	cmd := exec.Command("caddy", "reload", "--config", mainConfig)
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Fprintf(os.Stderr, "[cube-mcp] caddy reload failed: %v: %s\n", err, string(output))
	} else {
		fmt.Fprintf(os.Stderr, "[cube-mcp] caddy reloaded (routes: %s)\n", rm.caddyConfigPath)
	}
}

// ---- Tool handlers: Routing ----

func handleCreateRoute(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	domain := argString(args, "domain")
	if domain == "" {
		return errResult("domain is required"), nil
	}
	containerID := argString(args, "container_id")
	if containerID == "" {
		return errResult("container_id is required"), nil
	}
	targetPort := argInt(args, "target_port", 0)
	if targetPort == 0 {
		return errResult("target_port is required"), nil
	}
	pathPrefix := argString(args, "path_prefix")

	route, err := routeMgr.createRoute(domain, containerID, targetPort, pathPrefix)
	if err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(route), nil
}

func handleDeleteRoute(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	domain := argString(args, "domain")
	if domain == "" {
		return errResult("domain is required"), nil
	}
	if err := routeMgr.deleteRoute(domain); err != nil {
		return errResult(err.Error()), nil
	}
	return okResult(map[string]interface{}{"domain": domain, "status": "deleted"}), nil
}

func handleListRoutes(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	routes, err := routeMgr.listRoutes()
	if err != nil {
		return errResult(err.Error()), nil
	}
	if routes == nil {
		routes = []*Route{}
	}
	return okResult(routes), nil
}

func handleReloadRoutes(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Regenerate fragment from stored routes, then reload Caddy.
	if err := routeMgr.generateCaddyConfig(); err != nil {
		return errResult(err.Error()), nil
	}
	routeMgr.reloadCaddy()
	return okResult(map[string]interface{}{
		"status":  "reloaded",
		"config":  routeMgr.caddyConfigPath,
		"message": "Caddy config regenerated and reloaded",
	}), nil
}

// validateDomain ensures a domain name is safe to embed in a Caddy config file.
// Prevents config injection via newlines, braces, or other Caddy metacharacters (H4).
func validateDomain(domain string) error {
	if domain == "" {
		return fmt.Errorf("domain is required")
	}
	if len(domain) > 253 {
		return fmt.Errorf("domain too long (max 253 chars per RFC 1035)")
	}
	// Domain must be: labels separated by dots. Each label: alphanumeric + hyphens.
	// No whitespace, no newlines, no braces, no special chars.
	for _, c := range domain {
		if !(c >= 'a' && c <= 'z') && !(c >= 'A' && c <= 'Z') && !(c >= '0' && c <= '9') && c != '.' && c != '-' && c != '_' && c != ':' && c != '*' {
			return fmt.Errorf("domain contains invalid character %q (only alphanumeric, dots, hyphens, underscores, wildcards allowed)", c)
		}
	}
	// Reject newlines explicitly (covered by above but be explicit for readability)
	if strings.ContainsAny(domain, "\n\r	 {}();|&<>`$\\'\"") {
		return fmt.Errorf("domain contains forbidden characters (config injection prevention)")
	}
	return nil
}

// validatePathPrefix ensures a URL path prefix is safe for Caddy config.
func validatePathPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if len(prefix) > 500 {
		return fmt.Errorf("path prefix too long")
	}
	if strings.ContainsAny(prefix, "\n\r	 {};|&<>`$\\'\"") {
		return fmt.Errorf("path prefix contains forbidden characters")
	}
	return nil
}
