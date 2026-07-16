# Release v0.11.0-beta

## New: Bare-Metal System Tools (+17 tools)

The MCP server now manages the full bare-metal lifecycle, not just containers.
This enables managing a cluster from a phone via API without SSH access.

### systemd Services (4)
- `service_status` — List running services or get status of a specific one
- `service_restart` — Restart a systemd service
- `service_start` — Start a service
- `service_stop` — Stop a service

### Firewall / nftables (3)
- `firewall_list` — List all active firewall rules
- `firewall_add_rule` — Add a drop/accept rule (port, protocol, interface)
- `firewall_delete_rule` — Delete a rule by handle

### Kernel sysctl (2)
- `sysctl_get` — Read a kernel parameter
- `sysctl_set` — Set a kernel parameter at runtime

### System Info (1)
- `node_info` — CPU, RAM, disk, uptime, load average, kernel version

### Package Management (2)
- `package_install` — Install a package via apt-get
- `package_update` — Run apt-get update + upgrade

### File Operations (2)
- `file_read` — Read a file (sensitive paths blocked automatically)
- `file_write` — Write a file (sensitive paths protected)

### Network (2)
- `wireguard_status` — WireGuard VPN status (peers, handshakes, transfers)
- `network_interfaces` — List all network interfaces with IPs

### Multi-Node (1)
- `exec_on_node` — Execute a command on another cluster node via SSH

### RBAC
All new tools are integrated into the RBAC permission system:
- **Viewer**: node_info, service_status, firewall_list, sysctl_get, wireguard_status, network_interfaces, file_read
- **Operator**: service_restart/start/stop, package_install/update, file_write, firewall_add_rule, exec_on_node
- **Admin**: firewall_delete_rule, sysctl_set

### Security
- `file_read` and `file_write` block access to sensitive paths (totp-secret, auth-keys.json, secrets.key, wg0.conf, private keys, shadow files)
- `exec_on_node` validates target against known cluster nodes
- All operations go through the existing audit log

## Pre-compiled Binaries

This release includes pre-compiled static binaries — no Go toolchain required:
- `cube-mcp-v0.11.0-linux-amd64` — Linux x86_64 (statically linked)
- `cube-mcp-v0.11.0-linux-arm64` — Linux ARM64 (statically linked)

Install:
```bash
wget https://github.com/schwabauerbriantomas-gif/orchestrator-cube-container/releases/download/v0.11.0-beta/cube-mcp-v0.11.0-linux-amd64
chmod +x cube-mcp-v0.11.0-linux-amd64
./cube-mcp-v0.11.0-linux-amd64 -mode http -port 8080
```

## Stats
- Total tools: **178** (was 161)
- Total Go LOC added: ~500
- Tests: 17/17 passed end-to-end on Samsung SBB cluster
