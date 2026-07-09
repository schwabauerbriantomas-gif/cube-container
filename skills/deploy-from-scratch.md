# Skill: Deploy from Scratch

**Triggers:** When the user asks to deploy a new app, set up a service from nothing, "stand up" a container, get something running and reachable, deploy an image they already have, or asks "how do I deploy X".
**Prerequisites:** A reachable cluster (`cluster_health` returns ok), at least one node with free capacity, the base image available locally or pullable from a registry.
**RBAC Required:** operator (admin for `create_route`, `service_create`, `secret_set`)

## Workflow

1. **Pick a node** — `suggest_node` with `required_memory_mb`, `required_cpu_count`
   - Returns top-3 bin-packing candidates. Choose the highest-scored. If the cluster has only one node, skip this step.
2. **Create a template** — `create_template` with `image` (e.g. `python:3.12-slim`), `expose_ports` (the port(s) the app listens on internally), `writable_layer_size_gb`
   - Templates are reusable; check `list_templates` first to avoid duplicates.
3. **Create and start the container** — `create_container` with `template_id` (from step 2), `memory_mb`, `cpu_count`, `env_vars`, `metadata`
   - Returns `container_id`. The container is running immediately.
4. **Expose it** — `add_port_mapping` with `container_id`, `host_port`, `container_port`
   - Maps a host port so the service is reachable from outside the node.
5. **Route a domain (optional, needs DNS first)** — `create_route` with `domain`, `container_id`, `port`
   - Caddy auto-obtains a Let's Encrypt cert. **The domain's A record must already point at this server's IP** or cert issuance will fail.
6. **Add a health check** — `health_check_set` with `container_id`, `type` (http/tcp/exec), probe params
   - Enables auto-restart if the container wedges. **Do this before scaling** — the rollout tool gates on health probes.
7. **Scale into a service (optional)** — `service_create` with `name`, `template_id`, `port`, `domain`; then `scale_set` with `service_name`, `replicas`
   - `service_create` only defines the service; `scale_set` actually spawns replicas.
8. **Add an alert** — `alert_rule_add` with `type=container_down`, `container_id`, `severity=critical`
   - Fires if the container (and its auto-restart) can't keep it alive.

## Choosing the Deploy Entry Point

The single biggest decision is **which deploy tool to start with**:

- **`create_container`** — Use when you already have a built/pulled OCI image and just need it running. Fastest path. No source code involved. Steps 2–3 above.
- **`deploy_from_git`** — Use when the app lives in a git repo. Clones the repo into a persistent volume, builds a template, starts a container. Code survives restarts. Required args: `git_url`, `branch`, `image`, `build_command` (optional). Use this for any real application with source control.
- **`deploy_from_code`** — Use for inline / throwaway deploys where you pass file contents directly (no git). Files are written to a persistent volume. Good for scripts, one-off services, prototyping.
- **`deploy_to_node`** — Wrap any of the above when you want the container on a **specific remote node** instead of the local node. Always pair with `suggest_node` first.

Rule of thumb: real app with a repo → `deploy_from_git`. Quick image → `create_container`. Prototype with pasted code → `deploy_from_code`. Multi-node → `deploy_to_node`.

## Decision Points

- **Image not local?** Call `image_pull` before `create_template`, or let `deploy_from_git` handle the build via `image_build`.
- **Need the source to persist across restarts?** Use `deploy_from_git` or `deploy_from_code` — both write to a persistent volume. Plain `create_container` does not.
- **Domain given?** Run `create_route` after the container is up. No domain → stop at `add_port_mapping`.
- **Production?** Always set a health check (step 6) and a `container_down` alert (step 8) before considering the deploy done.
- **Need to scale beyond one replica?** Convert to a service (step 7). A bare `create_container` is not load-balanced.

## Error Recovery

- **`suggest_node` returns no candidates** → cluster is full. Either `scale_set` something down, `kill_container` on an unused one, or `node_add` a new node. Do not proceed with a deploy that won't fit.
- **`create_template` fails on image** → image isn't pullable. `image_pull` it explicitly, check the tag spelling, or `image_build` from a Dockerfile.
- **`create_container` fails with resource error** → lower `memory_mb`/`cpu_count` and retry, or `suggest_node` again for a bigger node.
- **`create_route` fails TLS** → DNS not propagated. Verify the A record points here, wait, then `reload_routes`. Do not leave a half-provisioned route — `delete_route` and retry.
- **`health_check_set` immediately fails** → the container's app isn't listening on the probed port, or the path is wrong. `get_container_logs` to see why, fix, then re-set the probe.

## Example Session

> **User:** Deploy my Python API. Repo is github.com/me/api, branch main, it listens on 8000. Put it at api.example.com.
>
> **Agent:**
> 1. `suggest_node` `{"required_memory_mb": 512, "required_cpu_count": 1}` → node-a (score 0.92)
> 2. `deploy_from_git` `{"git_url": "https://github.com/me/api", "branch": "main", "image": "python:3.12-slim", "node": "node-a"}` → container_id `c-7f3a`
> 3. `add_port_mapping` `{"container_id": "c-7f3a", "host_port": 8000, "container_port": 8000}`
> 4. `create_route` `{"domain": "api.example.com", "container_id": "c-7f3a", "port": 8000}`
> 5. `health_check_set` `{"container_id": "c-7f3a", "type": "http", "http_port": 8000, "http_path": "/health", "interval_seconds": 30, "failure_threshold": 3}`
> 6. `alert_rule_add` `{"id": "api-down", "name": "API container down", "type": "container_down", "container_id": "c-7f3a", "severity": "critical"}`
>
> Deployed. `api.example.com` is live with auto-restart and a down alert. Want me to scale it to 3 replicas?
