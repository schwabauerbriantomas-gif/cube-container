# Cube Container

[![Fork of TencentCloud/CubeSandbox](https://img.shields.io/badge/fork%20of-CubeSandbox-blue)](https://github.com/TencentCloud/CubeSandbox)
[![License: Apache 2.0](https://img.shields.io/badge/License-Apache_2.0-green)](LICENSE)
[![MCP Server](https://img.shields.io/badge/MCP-50%20tools-orange)](https://modelcontextprotocol.io)
[![Min RAM: 4GB](https://img.shields.io/badge/Min%20RAM-4GB-success)]()
[![Go Version](https://img.shields.io/badge/Go-1.24%2B-00ADD8)]()

**Container-mode fork of [CubeSandbox](https://github.com/TencentCloud/CubeSandbox)** — the same control plane, E2B-compatible API, and MCP orchestration, but running on **native Linux containers (containerd + runc + overlayfs)** instead of KVM MicroVMs.

Designed for **low-resource edge nodes** (4 GB RAM, 4 cores, no KVM) where hardware virtualization isn't available or is overkill.

---

## Why This Fork?

CubeSandbox is an excellent AI-agent sandbox runtime (8.7K+ stars on GitHub), but it requires:

- **KVM support** (`/dev/kvm`)
- **8+ GB RAM** per node
- **XFS with reflink** for Copy-on-Write storage
- A full hypervisor stack (rustvmm / Cloud Hypervisor)

**Cube Container** strips out the entire VM layer — `hypervisor/`, `cubecow/`, `CubeNet/`, `CubeShim/` — and runs everything as native containers. Same control plane, same API surface, same lifecycle management, but **10× lighter and 6× faster cold starts**.

### Target hardware

| Spec | CubeSandbox (upstream) | **Cube Container** |
|------|----------------------|---------------------|
| Min RAM per node | 8 GB | **4 GB** |
| Virtualization required | KVM (`/dev/kvm`) | **None** |
| CPU overhead per workload | ~50 MB (VM kernel + VMM) | **~2–5 MB** (process) |
| Cold start | ~60 ms (boot VM kernel) | **~5–10 ms** (clone + namespaces) |
| Auto-resume (pause/thaw) | ~100 ms (VM restore) | **~15 ms** (cgroup freezer) |

Runs on COTS mini-PCs, ARM SBCs, recycled office machines, Proxmox VMs, or any Linux host with containerd.

---

## What Changed

| Component | CubeSandbox (original) | Cube Container (this fork) |
|-----------|----------------------|---------------------------|
| **Isolation** | KVM MicroVM (hardware-level) | runc containers (namespace-level) |
| **Hypervisor** | `hypervisor/` (rustvmm, 30+ modules) | ❌ Removed — not needed |
| **Storage** | `cubecow/` (XFS reflink CoW) | `overlayfs` (containerd native) |
| **Networking** | `CubeNet/` (eBPF + vsock) | CNI bridge (standard) |
| **Runtime Shim** | `CubeShim/` (containerd → VM bridge) | ❌ Removed — runc is direct |
| **Storage plugin** | CubeCow + XFS only | Accepts `overlayfs` backend |

### What Stayed the Same

- ✅ **CubeAPI** (Rust) — E2B-compatible REST API on `:3000`
- ✅ **CubeMaster** (Go) — multi-node scheduler
- ✅ **Cubelet** (Go) — per-node lifecycle management
- ✅ **cube-lifecycle-manager** — auto-pause/resume logic
- ✅ **CubeProxy** (nginx) — reverse proxy + TLS
- ✅ **CubeEgress** (OpenResty) — egress control
- ✅ **network-agent** (Go) — network management
- ✅ **Web Console** (`:12088`)

### What Was Added

- ✅ **MCP Server** (Go) — **50 tools** for AI-agent-driven orchestration, single static binary
- ✅ **Dual Backend** — auto-detects Docker (production) or Cube (edge 4GB) at runtime
- ✅ **Auth + RBAC** (Go) — API-key + secret auth, RBAC (viewer/operator/admin), rate limiting, JSONL audit trail with SHA256 hash chain — built into MCP server
- ✅ **HA Active-Passive** — heartbeat-based failover with HMAC auth + priority-based split-brain resolution
- ✅ **Encrypted Secrets** — AES-256-GCM at rest, 3 key sources (hex key, passphrase, auto-generated)
- ✅ **Zero-Config TLS** — Caddy route generation with automatic Let's Encrypt certificates
- ✅ **Networking** — port mappings, DNS aliases, network policies (iptables-backed)
- ✅ **Backup & Restore** — tar.gz with SHA256 integrity, container manifests, point-in-time staging
- ✅ **Rollback** — deployment version history with git-based rollback
- ✅ **GitOps Webhook** — auto-deploy on push (GitHub, Gitea, GitLab, Gogs)
- ✅ **Metrics** — Prometheus `/metrics` endpoint with live cluster state
- ✅ **Log Streaming** — SSE endpoint for real-time container log tailing
- ✅ **4D Scheduling** — bin-packing node suggestions based on CPU, RAM, disk, network
- ✅ **Security Hardened** — 18 attack surfaces audited and closed (allowlist exec, SSRF prevention, timing-attack resistant auth, per-IP connection limits)

---

## Architecture

### Dual-mode operation

```
 ┌─────────────────────────────────────────────────────────────┐
 │                      LOCAL (trusted)                         │
 │                                                              │
 │  AI Agent ──stdio──▶ MCP Server ──▶ ContainerBackend         │
 │  (Hermes,           (Go, 50 tools)   ├── Docker (unix sock)  │
 │   Claude,                            └── Cube (HTTP :3000)   │
 │   Cursor)                                   │                │
 │                                              ▼                │
 │                              CubeMaster (Go)                  │
 │                              ├── Node 1 (Cubelet + runc)     │
 │                              ├── Node 2 (Cubelet + runc)     │
 │                              └── Node N (Cubelet + runc)     │
 └─────────────────────────────────────────────────────────────┘

 ┌─────────────────────────────────────────────────────────────┐
 │                   REMOTE (untrusted / production)            │
 │                                                              │
 │  External            Caddy :443 (or native TLS)              │
 │  Client ──HTTPS──▶  ├── TLS 1.3 / Let's Encrypt auto         │
 │  (API key)          ├── WAF (SQLi, XSS, path traversal)     │
 │                     ├── Rate limiting                        │
 │                     └── Dynamic route import                 │
 │                              │                               │
 │                              ▼                               │
 │                       MCP HTTP :8080                          │
 │                       ├── API-key + secret (HMAC compare)    │
 │                       ├── RBAC (viewer/operator/admin)       │
 │                       ├── Rate limiting (120 req/min/key)    │
 │                       ├── Audit trail (JSONL SHA256 chain)   │
 │                       ├── Body size limit (10 MB)            │
 │                       ├── Per-IP conn limit (64)             │
 │                       └── Secrets redaction in audit         │
 │                              │                               │
 │                              ▼                               │
 │                       ContainerBackend                        │
 └─────────────────────────────────────────────────────────────┘
```

### Backend auto-detection

The MCP server probes the environment at startup and selects the best backend:

```
1. CUBE_BACKEND=docker  → force Docker
2. CUBE_BACKEND=cube    → force Cube
3. /var/run/docker.sock responds → Docker (lower latency)
4. fallback             → Cube (lighter, for edge 4GB)
```

The model always knows which runtime is active via the `backend_info` tool.

### Security layer separation

| Layer | Where | Applies to |
|-------|-------|------------|
| Input validation (path traversal, git sanitization, **command allowlist**, SSRF prevention) | `security.go` — MCP server | **Both** modes |
| TLS 1.3 + WAF + rate limiting | `Caddyfile` — Caddy proxy | HTTP mode only |
| API-key auth + RBAC + audit | `auth.go` — built into MCP server | HTTP mode only |
| Encrypted secrets (AES-256-GCM) | `secrets.go` — MCP server | Both modes |
| HA heartbeat auth (HMAC-SHA256) | `ha.go` — MCP server | HTTP mode only |

This means: local stdio mode has zero auth overhead (it's a pipe), while HTTP mode gets full production-grade security.

---

## MCP Tools (50 total)

Any MCP-compatible AI agent (Claude, Cursor, Hermes, OpenAI agents, local LLMs) can manage the entire cluster through natural language.

### Cluster Management (6)

| Tool | Description |
|------|-------------|
| `cluster_health` | Check CubeAPI reachability |
| `cluster_overview` | Node count, running containers, resource capacity |
| `cluster_versions` | Component version matrix (CubeAPI, CubeMaster, Cubelet) |
| `list_nodes` | All nodes with CPU/RAM/disk info |
| `get_node` | Detailed node info by ID |
| `suggest_node` | 4D bin-packing: best node for new container based on CPU/RAM/disk/net |

### Container Lifecycle (8)

| Tool | Description |
|------|-------------|
| `list_containers` | Running / paused / stopped containers |
| `get_container` | Container details by ID |
| `create_container` | Deploy from template with CPU/RAM/env config |
| `kill_container` | Stop and remove |
| `pause_container` | Freeze (cgroup freezer, ~0 CPU, ~15ms to resume) |
| `resume_container` | Thaw a paused container |
| `get_container_logs` | Fetch stdout/stderr logs |
| `tail_container_logs` | Last N log lines (for real-time use SSE `/streams/{id}/logs`) |

### Templates (3)

| Tool | Description |
|------|-------------|
| `list_templates` | Available container templates |
| `create_template` | Create from any OCI image with port mappings |
| `get_template` | Template details |

### Persistent Deployment (4)

| Tool | Description |
|------|-------------|
| `deploy_from_git` | Clone repo, build image, deploy with env vars + volumes |
| `deploy_from_code` | Deploy from inline files (no git needed) |
| `update_code` | Pull latest from git and redeploy |
| `exec_in_container` | Run command inside a running container (allowlist-enforced) |

### Volumes (3)

| Tool | Description |
|------|-------------|
| `list_volumes` | Persistent volumes across the cluster |
| `create_volume` | Create a named volume |
| `delete_volume` | Remove a volume |

### Backup & Restore (5)

| Tool | Description |
|------|-------------|
| `backup_volume` | tar.gz with SHA256 integrity, point-in-time staging copy |
| `backup_container` | Full snapshot: config manifest + all mounted volumes |
| `list_backups` | All backups with size, checksum, restorable status |
| `restore_backup` | Restore with SHA256 verification before unpacking |
| `delete_backup` | Remove a backup permanently |

### Deployment Versioning (2)

| Tool | Description |
|------|-------------|
| `rollback_deploy` | Rollback to previous deployment version (git-based) |
| `list_deploy_versions` | List all deployment versions for an app |

### Routing & TLS (4)

| Tool | Description |
|------|-------------|
| `create_route` | Domain → container reverse proxy with automatic Let's Encrypt TLS |
| `delete_route` | Remove a domain route and its TLS certificate |
| `list_routes` | All configured domain routes with TLS status |
| `reload_routes` | Force regenerate Caddy config and reload |

### Networking (9)

| Tool | Description |
|------|-------------|
| `add_port_mapping` | Map host port to container port (iptables-backed) |
| `remove_port_mapping` | Remove a port mapping by ID |
| `list_port_mappings` | All port mappings |
| `add_dns_alias` | Add DNS alias to `/etc/hosts` (IP-validated) |
| `remove_dns_alias` | Remove a DNS alias |
| `list_dns_aliases` | All DNS aliases |
| `add_network_policy` | Allow/deny firewall rule between containers |
| `list_network_policies` | All network policies |
| `remove_network_policy` | Remove a network policy by ID |

### High Availability (1)

| Tool | Description |
|------|-------------|
| `ha_state` | Current HA state: role (active/standby), active node, peer health |

### Secrets Management (4)

| Tool | RBAC | Description |
|------|------|-------------|
| `secret_set` | admin | Store encrypted secret (AES-256-GCM) |
| `secret_get` | operator | Decrypt and retrieve a secret |
| `secret_list` | viewer | List secret names + metadata (no values) |
| `secret_delete` | admin | Permanently delete a secret |

### Backend Introspection (1)

| Tool | Description |
|------|-------------|
| `backend_info` | Active backend (docker/cube), endpoint, tool count, capabilities |

---

## Quick Start

### Build the MCP server from source

```bash
cd mcp-server-go
CGO_ENABLED=0 go build -o cube-mcp .
# Produces a single ~7MB static binary — no runtime dependencies
```

### Run in stdio mode (local, trusted)

```bash
./cube-mcp
# [cube-mcp] backend auto-detected → docker (or cube)
# [cube-mcp] stdio mode → backend=docker endpoint=/var/run/docker.sock
```

### Configure your MCP client

```json
{
  "mcpServers": {
    "cube-container": {
      "command": "cube-mcp",
      "env": {
        "CUBE_API_URL": "http://localhost:3000",
        "CUBE_API_KEY": "e2b_000000"
      }
    }
  }
}
```

### Run in HTTP mode (production, untrusted)

```bash
# Generate an admin API key
./cube-mcp --gen-key admin --label "production-admin"
# → Key:    cc_live_a1b2c3d4...
# → Secret: sec_e5f6g7h8...

# Start the server
./cube-mcp --mode http --port 8080

# Optionally with native TLS (bypass Caddy)
CUBE_TLS_CERT=/path/to/cert.pem \
CUBE_TLS_KEY=/path/to/key.pem \
./cube-mcp --mode http --port 8443
```

### Deploy a service from git (via MCP)

```
User: "Deploy the app at github.com/me/my-api on port 8000"

Agent → deploy_from_git(
    git_url="https://github.com/me/my-api",
    expose_ports=[8000],
    memory_mb=256
)
→ Container running at node-2:8000
```

### Expose with automatic TLS

```
User: "Route api.myapp.com to container abc123 on port 8000"

Agent → create_route(
    domain="api.myapp.com",
    container_id="abc123",
    target_port=8000
)
→ Caddy provisions Let's Encrypt certificate automatically
→ Route live at https://api.myapp.com
```

---

## Configuration

All configuration is via environment variables. No config files needed.

### Core

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_BACKEND` | auto | `docker`, `cube`, or `auto` (auto-detect) |
| `CUBE_API_URL` | `http://localhost:3000` | CubeAPI URL (Cube backend only) |
| `CUBE_API_KEY` | `e2b_000000` | CubeAPI key (Cube backend only) |
| `DOCKER_SOCKET` | `/var/run/docker.sock` | Docker socket path (Docker backend) |

### Security

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_AUTH_KEYS_FILE` | `/var/lib/cube-container/auth-keys.json` | API key store |
| `CUBE_AUDIT_LOG` | `/var/lib/cube-container/audit.logl` | Audit log path |
| `CUBE_EXEC_ALLOWLIST` | *(built-in list)* | Comma-separated extra allowed exec commands |
| `CUBE_ALLOW_INSECURE_GIT` | `false` | Allow `http://` and `git://` protocols (otherwise https+ssh only) |
| `CUBE_TLS_CERT` | *(empty)* | Path to TLS cert for native HTTPS |
| `CUBE_TLS_KEY` | *(empty)* | Path to TLS key for native HTTPS |

### Secrets

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_SECRETS_KEY` | *(empty)* | Hex-encoded 32-byte AES key (highest priority) |
| `CUBE_SECRETS_PASSPHRASE` | *(empty)* | Derive key from passphrase (PBKDF2, 100K iterations) |
| `CUBE_SECRETS_FILE` | `/var/lib/cube-container/secrets.json` | Encrypted secrets store |
| `CUBE_SECRETS_KEY_FILE` | `/var/lib/cube-container/keys/secrets.key` | Auto-generated key path |
| `CUBE_SECRETS_SALT_FILE` | `/var/lib/cube-container/keys/secrets.salt` | Auto-generated salt path |

### High Availability

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_HA_PEERS` | *(empty)* | Comma-separated `host:port` list of CubeMaster peers |
| `CUBE_HA_SELF_ID` | hostname | This node's unique ID |
| `CUBE_HA_ROLE` | auto | `active`, `standby`, or auto (active if no peers) |
| `CUBE_HA_PRIORITY` | `100` | Lower wins split-brain (0 = highest) |
| `CUBE_HA_SECRET` | *(empty)* | HMAC-SHA256 shared secret for heartbeat auth |

### Routing & TLS

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_CADDY_CONFIG_PATH` | `/etc/caddy/cube-routes.caddy` | Generated Caddy route fragment |
| `CUBE_CADDY_MAIN_CONFIG` | `/etc/caddy/Caddyfile` | Main Caddyfile for reload |
| `CUBE_CADDY_RELOAD` | `false` | Invoke `caddy reload` after route changes |
| `CUBE_ROUTES_ROOT` | `/var/lib/cube-container/routes` | Route JSON store |

### GitOps

| Variable | Default | Description |
|----------|---------|-------------|
| `CUBE_WEBHOOK_ENABLED` | `false` | Enable git webhook listener |
| `CUBE_WEBHOOK_SECRET` | *(empty)* | Webhook auth secret (HMAC compared) |

---

## Comparison with Alternatives

### Sandbox / Isolation Runtimes

| Feature | **Cube Container** | CubeSandbox (upstream) | E2B | Daytona | fly.io Machines |
|---------|-------------------|----------------------|-----|---------|----------------|
| **Isolation model** | runc containers | KVM MicroVM | gVisor Firecracker | Containers / MicroVM | Firecracker |
| **Min RAM per node** | **4 GB** | 8 GB | 8 GB (managed) | 8 GB | 4 GB (managed) |
| **Cold start** | **~5 ms** | ~60 ms | ~150 ms (cold) | ~5 s | ~300 ms |
| **Self-hosted** | ✅ | ✅ | ❌ (SaaS only) | ✅ | ❌ (managed) |
| **KVM required** | ❌ | ✅ | ✅ | Optional | N/A |
| **MCP support** | ✅ 50 tools | ❌ | ❌ | ❌ | ❌ |
| **AI-agent native** | ✅ (MCP, stdio+HTTP) | ❌ | SDK only | ❌ | ❌ |
| **Dual backend** | ✅ Docker + Cube | ❌ | ❌ | ❌ | ❌ |
| **Encrypted secrets** | ✅ AES-256-GCM | ❌ | ❌ | ❌ | ❌ |
| **HA failover** | ✅ Active-passive | ❌ | ❌ | ❌ | ❌ |
| **Best for** | Edge nodes, self-hosted services, AI agent ops | Untrusted LLM code execution | Managed code sandboxes | Dev environments | Managed global infra |

### Container Orchestration (K8s / Nomad / Docker Swarm)

| Feature | **Cube Container** | Kubernetes | Nomad | Docker Swarm |
|---------|-------------------|-----------|-------|-------------|
| **Complexity** | Low (single binary + MCP) | High (etcd, kubelet, CNI, CSI) | Medium | Low |
| **Min nodes** | 1 | 1 (but heavy) | 1 | 1 |
| **Min RAM (control plane)** | **~8 MB** | ~1 GB | ~200 MB | ~100 MB |
| **MCP orchestration** | ✅ (native) | Via 3rd-party tools | ❌ | ❌ |
| **Auto-pause idle** | ✅ (~15 ms resume) | ❌ | ❌ | ❌ |
| **Git-driven deploy** | ✅ (`deploy_from_git`) | ArgoCD / Flux (separate) | Templates | ❌ |
| **Learning curve** | Low | Steep | Medium | Low |
| **Best for** | Edge, AI-agent ops, small clusters | Enterprise, large-scale | Hybrid workloads | Simple stacks |

Cube Container is **not trying to replace Kubernetes**. It targets a different niche: small clusters (1–10 nodes) on resource-constrained hardware where K8s is overkill, but you still need multi-node scheduling, lifecycle management, and MCP-native AI-agent control.

---

## Security Model

### Security Trade-off (important)

| | CubeSandbox (KVM) | Cube Container (runc) |
|---|---|---|
| Isolation strength | Hardware (dedicated kernel) | Namespace (shared kernel) |
| Container escape risk | Near zero | Low but nonzero |
| Best for | Untrusted LLM-generated code | **Trusted workloads and services** |

**Cube Container is designed for hosting your own services** (APIs, static sites, workers, bots) where you control what runs inside the containers. It is **not suitable for running untrusted user-submitted code**.

For untrusted workloads, use upstream [CubeSandbox](https://github.com/TencentCloud/CubeSandbox) with KVM.

### Security Audit

The MCP server underwent a full attack-surface audit. **18 issues were identified and all 18 are resolved:**

| Severity | Count | Status |
|----------|-------|--------|
| 🔴 Critical | 4 | ✅ All fixed |
| 🟠 High | 5 | ✅ All fixed |
| 🟡 Medium | 5 | ✅ All fixed |
| 🟢 Low | 4 | ✅ All fixed |

Key hardening measures:

| Control | Implementation |
|---------|----------------|
| Transport encryption | TLS 1.3 via Caddy or native TLS (`CUBE_TLS_CERT`/`CUBE_TLS_KEY`) |
| WAF | OWASP Top-10 rules (SQLi, XSS, path traversal, command injection) |
| Authentication | API key + secret pair, **HMAC constant-time compare** (no timing oracle) |
| Authorization | RBAC: viewer (read-only), operator (deploy/manage), admin (full) |
| Rate limiting | 120 req/min per API key (sliding window) |
| **Exec allowlist** | Commands validated against an **allowlist** (not a bypassable blacklist) |
| **SSRF prevention** | Git URLs to private IPs / cloud metadata blocked |
| **Git injection** | Branch names validated, `--` separator prevents option injection |
| **Config injection** | Domain/path inputs sanitized (prevents Caddy config injection) |
| **Hosts injection** | DNS target validated as IP (prevents `/etc/hosts` injection) |
| **Body limit** | 10 MB max request body |
| **Conn limit** | 64 simultaneous connections per IP |
| Secrets encryption | AES-256-GCM at rest, nonce prepended, 3 key sources |
| Audit logging | JSONL append-only with SHA256 tamper-evident hash chain |
| Secret redaction | Plaintext secrets never written to audit log |
| HA heartbeat auth | HMAC-SHA256 shared secret, priority-based split-brain resolution |

---

## Performance

Real measurements from test environment (AMD Ryzen 5 3400G, Go 1.24, Linux):

| Metric | Value | Notes |
|--------|-------|-------|
| **Binary size** | 6.95 MB | Static binary, stripped (`-ldflags -s -w`) |
| **RSS memory (idle)** | 8.3 MB | HTTP mode, after startup |
| **Startup + init** | < 1 ms | Process spawn → MCP initialize response |
| **HTTP latency (avg)** | 482 µs | Full stack: auth → RBAC → rate limit → MCP → backend |
| **HTTP latency (p99)** | 589 µs | 100 requests, same stack |
| **Throughput** | 2,076 RPS | Requests per second, single core |

Component-level benchmarks (Go `testing.B`):

| Operation | Latency | Notes |
|-----------|---------|-------|
| Auth key validation | **110 ns** | HMAC compare, map lookup, timing-safe |
| RBAC permission check | **12 ns** | Map lookup + int compare |
| Rate limiter check | 244 µs | Sliding window |
| Audit log write | 5.2 µs | JSONL + SHA256 hash chain |
| Secrets encrypt (AES-256-GCM) | ~3 µs | Per secret |

All measurements taken with Go 1.24 on Linux/amd64. Reproducible via:
```bash
cd mcp-server-go
go test -tags=e2e -bench=. -benchmem ./...
```

---

## Project Structure

```
cube-container/
├── CubeAPI/                   # Rust — E2B-compatible REST API (:3000)
├── CubeMaster/                # Go — multi-node scheduler
├── Cubelet/                   # Go — per-node lifecycle (modified for overlayfs)
├── cube-lifecycle-manager/    # Auto-pause/resume controller
├── CubeProxy/                 # nginx reverse proxy + TLS
├── web/                       # React web console (:12088)
├── mcp-server-go/             # Go — MCP server (50 tools, auth, single static binary)
│   ├── server.go              # MCP server — dual stdio + HTTP mode, 50 tool handlers
│   ├── client.go              # CubeAPI HTTP client (Cube backend)
│   ├── docker_client.go       # Docker Engine API client (Docker backend)
│   ├── backend.go             # ContainerBackend interface + auto-detection
│   ├── deploy.go              # Persistent deploy from git/code
│   ├── security.go            # Input validation (allowlist exec, SSRF, git URL, branch)
│   ├── auth.go                # API-key auth, RBAC, rate limiting, audit, conn limits
│   ├── secrets.go             # AES-256-GCM encrypted secrets management
│   ├── ha.go                  # Active-passive HA with HMAC heartbeats
│   ├── backup.go              # Backup & restore with SHA256 integrity
│   ├── rollback.go            # Deployment version history & rollback
│   ├── routing.go             # Caddy route management + auto TLS
│   ├── networking.go          # Port mappings, DNS aliases, network policies
│   ├── webhook.go             # GitOps webhook listener (opt-in)
│   ├── metrics.go             # Prometheus /metrics endpoint
│   ├── scheduler.go           # 4D bin-packing node suggestions
│   ├── logstream.go           # SSE log streaming
│   ├── *_test.go              # 43 tests (security, auth, backup, concurrency, e2e, bench)
│   └── go.mod
├── deploy/
│   └── container-mode/        # Dockerfile, Caddyfile, config
└── sdk/                       # Python + Go SDKs
```

---

## Development

### Run the MCP server locally (stdio mode)

```bash
cd mcp-server-go
go build -o cube-mcp .
./cube-mcp
```

### Run tests (with race detector)

```bash
cd mcp-server-go && go test -race -timeout 60s ./...
```

### CI

6 jobs on GitHub Actions:
- Go tests (1.24 + 1.25) with race detector
- Cubelet build (Go)
- CubeMaster build (Go)
- Docker image build
- Security scan (Gosec)

See [CONTRIBUTING.md](CONTRIBUTING.md) for development guidelines.

---

## Roadmap

- [x] ~~Multi-node auto-discovery~~ → manual node registration
- [x] ~~WebSocket-based log streaming~~ → SSE `/streams/{id}/logs`
- [x] ~~Container resource metrics~~ → Prometheus `/metrics`
- [x] ~~Preemptive scheduling~~ → `suggest_node` 4D bin-packing
- [x] ~~Snapshot/rollback~~ → `rollback_deploy` + version history
- [x] ~~Webhook notifications~~ → GitOps webhook listener
- [ ] Scale: replicas + load balancer
- [ ] Billing / metering
- [ ] OIDC / OAuth2 authentication
- [ ] Multi-region federation

---

## License

Apache 2.0 (inherited from upstream [CubeSandbox](https://github.com/TencentCloud/CubeSandbox) / Tencent Cloud).

---

## Credits

- **[TencentCloud/CubeSandbox](https://github.com/TencentCloud/CubeSandbox)** — original project, 8.7K+ stars. This fork would not exist without their work.
- Built on [containerd](https://containerd.io/), [runc](https://github.com/opencontainers/runc), [Caddy](https://caddyserver.com/), [Docker Engine API](https://docs.docker.com/engine/api/), and the [MCP protocol](https://modelcontextprotocol.io/).
