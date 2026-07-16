// Package main: registration for bare-metal system administration tools.
// These extend the MCP server beyond container orchestration to cover
// the full bare-metal management lifecycle.
package main

import (
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// registerMetalTools adds system-level tools for bare-metal management.
// These tools allow managing systemd, firewall, kernel, packages, files,
// WireGuard, and network without SSH access.
func registerMetalTools(s *server.MCPServer) {
	// --- systemd services (4) ---
	registerTool(s, toolWithArgs("service_status",
		"Get status of a systemd service. If no service name is given, lists all running services.",
		mcp.WithString("service", mcp.Description("Service name (e.g. nginx, docker, sshd)")),
	), handleServiceStatus)
	registerTool(s, toolWithArgs("service_restart",
		"Restart a systemd service.",
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
	), handleServiceRestart)
	registerTool(s, toolWithArgs("service_start",
		"Start a systemd service.",
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
	), handleServiceStart)
	registerTool(s, toolWithArgs("service_stop",
		"Stop a systemd service.",
		mcp.WithString("service", mcp.Required(), mcp.Description("Service name")),
	), handleServiceStop)

	// --- Firewall nftables (3) ---
	registerTool(s, tool("firewall_list",
		"List all nftables firewall rules currently active on this node.",
	), handleFirewallList)
	registerTool(s, toolWithArgs("firewall_add_rule",
		"Add a firewall rule to block or allow traffic on a specific port/interface. Default: drop on enp4s0f0.",
		mcp.WithString("port", mcp.Required(), mcp.Description("Port number (e.g. 443, 3000)")),
		mcp.WithString("protocol", mcp.Description("tcp or udp (default: tcp)")),
		mcp.WithString("action", mcp.Description("drop or accept (default: drop)")),
		mcp.WithString("interface", mcp.Description("Network interface (e.g. enp4s0f0)")),
		mcp.WithString("chain", mcp.Description("Chain name (default: input)")),
		mcp.WithString("table", mcp.Description("Table name (default: cube_filter)")),
		mcp.WithString("family", mcp.Description("Address family (default: inet)")),
	), handleFirewallAddRule)
	registerTool(s, toolWithArgs("firewall_delete_rule",
		"Delete a specific firewall rule by its handle number.",
		mcp.WithString("handle", mcp.Required(), mcp.Description("Rule handle number (get from firewall_list)")),
	), handleFirewallDeleteRule)

	// --- Kernel sysctl (2) ---
	registerTool(s, toolWithArgs("sysctl_get",
		"Get the current value of a kernel parameter.",
		mcp.WithString("key", mcp.Required(), mcp.Description("Kernel parameter (e.g. net.ipv4.ip_forward)")),
	), handleSysctlGet)
	registerTool(s, toolWithArgs("sysctl_set",
		"Set a kernel parameter value at runtime.",
		mcp.WithString("key", mcp.Required(), mcp.Description("Kernel parameter")),
		mcp.WithString("value", mcp.Required(), mcp.Description("New value")),
	), handleSysctlSet)

	// --- Node info (1) ---
	registerTool(s, tool("node_info",
		"Get detailed system information: CPU, RAM, disk, uptime, load average, kernel version.",
	), handleNodeInfo)

	// --- Package management (2) ---
	registerTool(s, toolWithArgs("package_install",
		"Install a package via apt-get.",
		mcp.WithString("package", mcp.Required(), mcp.Description("Package name (e.g. nginx, htop)")),
	), handlePackageInstall)
	registerTool(s, tool("package_update",
		"Run apt-get update + upgrade on this node. Updates all packages to latest.",
	), handlePackageUpdate)

	// --- File operations (2) ---
	registerTool(s, toolWithArgs("file_read",
		"Read contents of a file on the node. Sensitive paths (secrets, keys) are blocked.",
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path")),
		mcp.WithNumber("limit", mcp.Description("Max lines to return (default 500)")),
	), handleFileRead)
	registerTool(s, toolWithArgs("file_write",
		"Write content to a file on the node. Sensitive paths are protected.",
		mcp.WithString("path", mcp.Required(), mcp.Description("Absolute file path")),
		mcp.WithString("content", mcp.Required(), mcp.Description("File content")),
	), handleFileWrite)

	// --- WireGuard (1) ---
	registerTool(s, tool("wireguard_status",
		"Get WireGuard VPN status: interfaces, peers, handshakes, transfer stats.",
	), handleWireguardStatus)

	// --- Network interfaces (1) ---
	registerTool(s, tool("network_interfaces",
		"List all network interfaces with their IP addresses and status.",
	), handleNetworkInterfaces)

	// --- Multi-node exec (1) ---
	registerTool(s, toolWithArgs("exec_on_node",
		"Execute a shell command on another cluster node via SSH. Node must be a known cluster member.",
		mcp.WithString("node", mcp.Required(), mcp.Description("Target node: debian1, debian2, debian3, or WireGuard IP")),
		mcp.WithString("command", mcp.Required(), mcp.Description("Shell command to execute")),
	), handleExecOnNode)
}
