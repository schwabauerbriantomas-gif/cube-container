// Package main: bare-metal system administration tools.
// Extends the MCP server with system-level operations that go beyond
// container orchestration: systemd services, firewall (nftables),
// kernel sysctl, package management, file operations, and WireGuard VPN.
//
// These tools allow managing the cluster from bare metal up without SSH access,
// enabling the "manage from phone" use case via the MCP API.
package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
)

// ============================================================
// systemd service management
// ============================================================

func handleServiceStatus(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	serviceName := argString(args, "service")
	if serviceName == "" {
		// List all active services
		out, err := runCommand("systemctl", "list-units", "--type=service", "--state=running", "--no-legend", "--no-pager")
		if err != nil {
			return errResult(fmt.Sprintf("failed to list services: %v", err)), nil
		}
		lines := strings.Split(strings.TrimSpace(out), "\n")
		services := make([]map[string]string, 0, len(lines))
		for _, line := range lines {
			if line == "" {
				continue
			}
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				services = append(services, map[string]string{
					"unit":    fields[0],
					"load":    fields[1],
					"active":  fields[2],
					"sub":     fields[3],
				})
			}
		}
		return okResult(map[string]interface{}{
			"services": services,
			"count":    len(services),
		}), nil
	}

	out, err := runCommand("systemctl", "status", serviceName, "--no-pager", "-l")
	if err != nil {
		return errResult(fmt.Sprintf("service '%s' not found or error: %v", serviceName, err)), nil
	}
	active, _ := runCommand("systemctl", "is-active", serviceName)
	enabled, _ := runCommand("systemctl", "is-enabled", serviceName)
	return okResult(map[string]interface{}{
		"service": serviceName,
		"active":  strings.TrimSpace(active),
		"enabled": strings.TrimSpace(enabled),
		"details": out,
	}), nil
}

func handleServiceRestart(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	serviceName := argString(args, "service")
	if serviceName == "" {
		return errResult("service is required"), nil
	}
	out, err := runCommand("systemctl", "restart", serviceName)
	if err != nil {
		return errResult(fmt.Sprintf("failed to restart %s: %s (output: %s)", serviceName, err, out)), nil
	}
	active, _ := runCommand("systemctl", "is-active", serviceName)
	return okResult(map[string]interface{}{
		"service": serviceName,
		"action":  "restarted",
		"status":  strings.TrimSpace(active),
	}), nil
}

func handleServiceStart(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	serviceName := argString(args, "service")
	if serviceName == "" {
		return errResult("service is required"), nil
	}
	_, err := runCommand("systemctl", "start", serviceName)
	if err != nil {
		return errResult(fmt.Sprintf("failed to start %s: %v", serviceName, err)), nil
	}
	active, _ := runCommand("systemctl", "is-active", serviceName)
	return okResult(map[string]interface{}{
		"service": serviceName,
		"action":  "started",
		"status":  strings.TrimSpace(active),
	}), nil
}

func handleServiceStop(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	serviceName := argString(args, "service")
	if serviceName == "" {
		return errResult("service is required"), nil
	}
	_, err := runCommand("systemctl", "stop", serviceName)
	if err != nil {
		return errResult(fmt.Sprintf("failed to stop %s: %v", serviceName, err)), nil
	}
	active, _ := runCommand("systemctl", "is-active", serviceName)
	return okResult(map[string]interface{}{
		"service": serviceName,
		"action":  "stopped",
		"status":  strings.TrimSpace(active),
	}), nil
}

// ============================================================
// Firewall (nftables)
// ============================================================

func handleFirewallList(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runCommand("nft", "list", "ruleset")
	if err != nil {
		return errResult(fmt.Sprintf("failed to list firewall rules: %v", err)), nil
	}
	ruleCount := strings.Count(out, "rule")
	return okResult(map[string]interface{}{
		"rules":  out,
		"count":  ruleCount,
		"engine": "nftables",
	}), nil
}

func handleFirewallAddRule(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	chain := argString(args, "chain")
	if chain == "" {
		chain = "input"
	}
	family := argString(args, "family")
	if family == "" {
		family = "inet"
	}
	table := argString(args, "table")
	if table == "" {
		table = "cube_filter"
	}
	port := argString(args, "port")
	protocol := argString(args, "protocol")
	if protocol == "" {
		protocol = "tcp"
	}
	action := argString(args, "action")
	if action == "" {
		action = "drop"
	}
	iface := argString(args, "interface")

	if port == "" {
		return errResult("port is required"), nil
	}

	var cmdParts []string
	cmdParts = append(cmdParts, "add", "rule", family, table, chain)
	if iface != "" {
		cmdParts = append(cmdParts, "iifname", iface)
	}
	cmdParts = append(cmdParts, protocol, "dport", port, action)

	out, err := runCommand("nft", cmdParts...)
	if err != nil {
		return errResult(fmt.Sprintf("failed to add firewall rule: %s (output: %s)", err, out)), nil
	}
	return okResult(map[string]interface{}{
		"action":   "rule_added",
		"family":   family,
		"table":    table,
		"chain":    chain,
		"port":     port,
		"protocol": protocol,
		"interface": iface,
		"target":   action,
	}), nil
}

func handleFirewallDeleteRule(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	handle := argString(args, "handle")
	if handle == "" {
		return errResult("handle is required (get it from firewall_list_rules)"), nil
	}
	out, err := runCommand("nft", "delete", "rule", "inet", "cube_filter", "input", "handle", handle)
	if err != nil {
		return errResult(fmt.Sprintf("failed to delete rule: %s (output: %s)", err, out)), nil
	}
	return okResult(map[string]interface{}{
		"action": "rule_deleted",
		"handle": handle,
	}), nil
}

// ============================================================
// Kernel sysctl
// ============================================================

func handleSysctlGet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	key := argString(args, "key")
	if key == "" {
		return errResult("key is required (e.g. net.ipv4.ip_forward)"), nil
	}
	out, err := runCommand("sysctl", "-n", key)
	if err != nil {
		return errResult(fmt.Sprintf("failed to get sysctl %s: %v", key, err)), nil
	}
	return okResult(map[string]interface{}{
		"key":   key,
		"value": strings.TrimSpace(out),
	}), nil
}

func handleSysctlSet(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	key := argString(args, "key")
	value := argString(args, "value")
	if key == "" || value == "" {
		return errResult("key and value are required"), nil
	}
	out, err := runCommand("sysctl", "-w", fmt.Sprintf("%s=%s", key, value))
	if err != nil {
		return errResult(fmt.Sprintf("failed to set sysctl %s=%s: %s (output: %s)", key, value, err, out)), nil
	}
	return okResult(map[string]interface{}{
		"key":    key,
		"value":  value,
		"action": "set",
	}), nil
}

// ============================================================
// Node system info
// ============================================================

func handleNodeInfo(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	hostname, _ := runCommand("hostname")
	kernel, _ := runCommand("uname", "-r")
	osRelease, _ := readFile("/etc/os-release")
	distro := extractDistro(osRelease)

	// CPU
	cpuInfo, _ := readFile("/proc/cpuinfo")
	cpuCores := strings.Count(cpuInfo, "processor")
	cpuModel := extractCPUModel(cpuInfo)

	// Memory
	memInfo, _ := readFile("/proc/meminfo")
	memTotal, memAvail := parseMemInfo(memInfo)

	// Disk
	diskOut, _ := runCommand("df", "-h", "/")
	diskInfo := parseDfOutput(diskOut)

	// Load
	loadAvg, _ := readFile("/proc/loadavg")
	loadParts := strings.Fields(loadAvg)

	// Uptime
	uptimeRaw, _ := readFile("/proc/uptime")
	uptimeParts := strings.Fields(uptimeRaw)
	uptimeSeconds := 0.0
	if len(uptimeParts) > 0 {
		uptimeSeconds, _ = strconv.ParseFloat(uptimeParts[0], 64)
	}

	result := map[string]interface{}{
		"hostname": strings.TrimSpace(hostname),
		"kernel":   strings.TrimSpace(kernel),
		"distro":   distro,
		"cpu": map[string]interface{}{
			"cores": cpuCores,
			"model": cpuModel,
		},
		"memory": map[string]interface{}{
			"total_mb":   memTotal,
			"avail_mb":   memAvail,
			"used_pct":   int(float64(memTotal-memAvail) / float64(memTotal) * 100),
		},
		"disk": diskInfo,
		"uptime_seconds": int(uptimeSeconds),
	}
	if len(loadParts) >= 3 {
		result["load"] = map[string]interface{}{
			"1m":  loadParts[0],
			"5m":  loadParts[1],
			"15m": loadParts[2],
		}
	}
	return okResult(result), nil
}

// ============================================================
// Package management (apt)
// ============================================================

func handlePackageInstall(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	pkg := argString(args, "package")
	if pkg == "" {
		return errResult("package is required"), nil
	}
	out, err := runCommand("apt-get", "install", "-y", "-qq", pkg)
	if err != nil {
		return errResult(fmt.Sprintf("failed to install %s: %s (output: %s)", pkg, err, out)), nil
	}
	return okResult(map[string]interface{}{
		"package": pkg,
		"action":  "installed",
	}), nil
}

func handlePackageUpdate(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runCommand("apt-get", "update", "-qq")
	if err != nil {
		return errResult(fmt.Sprintf("apt-get update failed: %s (output: %s)", err, out)), nil
	}
	out2, err2 := runCommand("apt-get", "upgrade", "-y", "-qq")
	if err2 != nil {
		return errResult(fmt.Sprintf("apt-get upgrade failed: %s (output: %s)", err2, out2)), nil
	}
	return okResult(map[string]interface{}{
		"action": "system_updated",
		"output": out2,
	}), nil
}

// ============================================================
// File operations
// ============================================================

func handleFileRead(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	path := argString(args, "path")
	if path == "" {
		return errResult("path is required"), nil
	}
	// Block reading secrets
	if isSensitivePath(path) {
		return errResult("access denied: path contains sensitive data"), nil
	}
	content, err := readFile(path)
	if err != nil {
		return errResult(fmt.Sprintf("failed to read %s: %v", path, err)), nil
	}
	limit := argInt(args, "limit", 500)
	lines := strings.Split(content, "\n")
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return okResult(map[string]interface{}{
		"path":     path,
		"content":  strings.Join(lines, "\n"),
		"lines":    len(lines),
		"truncated": len(strings.Split(content, "\n")) > limit,
	}), nil
}

func handleFileWrite(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	path := argString(args, "path")
	content := argString(args, "content")
	if path == "" || content == "" {
		return errResult("path and content are required"), nil
	}
	// Block overwriting secrets
	if isSensitivePath(path) {
		return errResult("access denied: path is protected"), nil
	}
	err := writeFile(path, content)
	if err != nil {
		return errResult(fmt.Sprintf("failed to write %s: %v", path, err)), nil
	}
	return okResult(map[string]interface{}{
		"path":    path,
		"action":  "written",
		"bytes":   len(content),
	}), nil
}

// ============================================================
// WireGuard management
// ============================================================

func handleWireguardStatus(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runCommand("wg", "show", "all")
	if err != nil {
		return errResult(fmt.Sprintf("failed to get WireGuard status: %v", err)), nil
	}
	if strings.TrimSpace(out) == "" {
		return okResult(map[string]interface{}{
			"status":  "inactive",
			"message": "WireGuard is not running",
		}), nil
	}
	return okResult(map[string]interface{}{
		"status": "active",
		"output": out,
	}), nil
}

// ============================================================
// Network interfaces
// ============================================================

func handleNetworkInterfaces(_ context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	out, err := runCommand("ip", "-json", "addr", "show")
	if err != nil {
		// Fallback to text format
		out, err := runCommand("ip", "addr", "show")
		if err != nil {
			return errResult(fmt.Sprintf("failed to get network interfaces: %v", err)), nil
		}
		return okResult(map[string]interface{}{
			"interfaces": out,
			"format":     "text",
		}), nil
	}
	return okResult(map[string]interface{}{
		"interfaces": out,
		"format":     "json",
	}), nil
}

// ============================================================
// Hosts management — run command on remote node via SSH
// ============================================================

func handleExecOnNode(_ context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args := parseArgs(req)
	node := argString(args, "node")
	command := argString(args, "command")
	if node == "" || command == "" {
		return errResult("node and command are required"), nil
	}
	// Validate node is a known cluster member
	if !isValidNode(node) {
		return errResult(fmt.Sprintf("unknown node: %s", node)), nil
	}
	// Execute via SSH
	out, err := runCommand("ssh", "-o", "ConnectTimeout=5", "-o", "StrictHostKeyChecking=no", node, command)
	if err != nil {
		return errResult(fmt.Sprintf("command failed on %s: %s (output: %s)", node, err, out)), nil
	}
	return okResult(map[string]interface{}{
		"node":    node,
		"command": command,
		"output":  out,
	}), nil
}

// ============================================================
// Helpers
// ============================================================

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func readFile(path string) (string, error) {
	cmd := exec.Command("cat", path)
	output, err := cmd.Output()
	return string(output), err
}

func writeFile(path, content string) error {
	cmd := exec.Command("tee", path)
	cmd.Stdin = strings.NewReader(content)
	return cmd.Run()
}

func isSensitivePath(path string) bool {
	lower := strings.ToLower(path)
	sensitive := []string{
		"totp-secret", "auth-keys.json", "secrets.key", "wg0.conf",
		"cube.env", "/etc/shadow", "/etc/gshadow",
		".ssh/id_", "private_key", "private.key",
	}
	for _, s := range sensitive {
		if strings.Contains(lower, s) {
			return true
		}
	}
	return false
}

func isValidNode(node string) bool {
	valid := []string{"debian1", "debian2", "debian3",
		"10.100.0.1", "10.100.0.2", "10.100.0.3"}
	for _, v := range valid {
		if node == v {
			return true
		}
	}
	return false
}

func extractDistro(osRelease string) string {
	for _, line := range strings.Split(osRelease, "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			return strings.Trim(strings.TrimPrefix(line, "PRETTY_NAME="), `"`)
		}
	}
	return "unknown"
}

func extractCPUModel(cpuInfo string) string {
	for _, line := range strings.Split(cpuInfo, "\n") {
		if strings.HasPrefix(line, "model name") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) == 2 {
				return strings.TrimSpace(parts[1])
			}
		}
	}
	return "unknown"
}

func parseMemInfo(memInfo string) (totalMB, availMB int) {
	for _, line := range strings.Split(memInfo, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			val, _ := strconv.Atoi(fields[1])
			val = val / 1024 // KB to MB
			if strings.HasPrefix(line, "MemTotal:") {
				totalMB = val
			}
			if strings.HasPrefix(line, "MemAvailable:") {
				availMB = val
			}
		}
	}
	return
}

func parseDfOutput(out string) map[string]interface{} {
	lines := strings.Split(out, "\n")
	if len(lines) >= 2 {
		fields := strings.Fields(lines[1])
		if len(fields) >= 6 {
			return map[string]interface{}{
				"filesystem": fields[0],
				"size":       fields[1],
				"used":       fields[2],
				"avail":      fields[3],
				"use_pct":    fields[4],
				"mount":      fields[5],
			}
		}
	}
	return map[string]interface{}{"raw": out}
}

// Ensure runtime import is used (for potential future concurrency)
var _ = runtime.NumCPU
