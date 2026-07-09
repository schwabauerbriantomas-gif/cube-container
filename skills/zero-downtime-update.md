# Skill: Zero-Downtime Update

**Triggers:** When the user asks to update an app without downtime, push a new image/version live safely, do a rolling update, blue-green deploy, or "update without taking it down".
**Prerequisites:** An existing **service** (created via `service_create`) with 2+ replicas, and a **health check** configured on the service's containers. The service must already be running the old image.
**RBAC Required:** operator (admin for `rollback_deploy`)

## Workflow

1. **Confirm the service exists and is healthy** — `service_get` with `name`
   - Check `replicas` ≥ 2 and that `health_check_list` shows the containers as healthy. A rolling update on a single replica is not zero-downtime.
2. **Run the rollout** — `deploy_rollout` with `service_name`, `new_image`, `strategy`, `health_wait_seconds`, `abort_on_failure`
   - This is the only tool you need for the actual update. It replaces replicas, gates each on its health probe, and aborts on failure.
3. **Verify** — `service_get` again, plus `health_check_list`
   - All replicas should be on the new image and healthy.

## Strategy Selection

- **`strategy: "rolling"` (default)** — Replaces replicas one-by-one. For each replica: create new → wait for health → kill old. Traffic stays up the whole time because at least one healthy replica is always serving. **Use this as the default.** Requires replicas ≥ 2 for true zero-downtime.
- **`strategy: "blue-green"`** — Creates ALL new replicas first, health-checks all of them, then switches traffic and kills the old set. **Use when:** you need to switch atomically (all-or-nothing), the new version is risky/incompatible and you want to validate every replica before any traffic moves, or you have enough capacity to run 2x the replicas briefly.

## Health Gate Behavior

`deploy_rollout` waits up to `health_wait_seconds` (default 60, max 600) for each new replica to pass its health check before proceeding. This is why the **health check must be configured before the rollout** — without one, there is no gate and a bad image ships instantly.

- For rolling: the old replica is killed only AFTER the new one is healthy.
- For blue-green: traffic switches only AFTER all new replicas are healthy.

## Abort Conditions

With `abort_on_failure: true` (default), the rollout stops immediately when a new replica fails to become healthy within `health_wait_seconds`. Already-replaced replicas stay (they passed their gate and are healthy). The result object includes `aborted: true` and `abort_reason`.

When aborted:
- The service is left in a **mixed state** — some replicas on the new image (healthy), some still on the old image.
- This is safe: every serving replica is healthy. Traffic continues.
- Your next move is the **rollback path** below.

## Rollback Path

If a rollout aborts or the new version misbehaves after shipping:

1. `rollback_deploy` with `app`/`container` identifier — redeploys from the prior version. **Admin role required.**
2. Confirm with `list_deploy_versions` to see version history and `service_get` to confirm replicas are back on the old image.

For git-based deploys, `rollback_deploy` redeploys from the prior git commit. For image-based deploys, it reverts to the previous image tag.

## Decision Points

- **Single replica?** A rolling update will have a brief gap. Either `scale_set` to ≥ 2 first, or use `blue-green` (but blue-green on one replica still needs capacity for 2 during the switch). If zero downtime is non-negotiable, scale up first.
- **No health check on the service?** Set one with `health_check_set` BEFORE the rollout. Without it, `abort_on_failure` has nothing to gate on.
- **Risky/incompatible change?** Use `blue-green` so you can validate all new replicas before any traffic moves.
- **Rollout aborted?** Don't retry blindly. `get_container_logs` on the failed new replica, fix the image, then either re-run `deploy_rollout` or `rollback_deploy`.
- **`health_wait_seconds` too short?** Slow-starting apps (JVM, large frameworks) may need 120–300s. Max is 600.

## Error Recovery

- **`deploy_rollout` errors with "service not found"** → the target isn't a service. It's a standalone container. You can't rolling-update a standalone container; convert it to a service first (`service_create` + `scale_set`) or just `kill_container` + redeploy (not zero-downtime).
- **Rollout aborts on first replica** → the new image is broken. `get_container_logs` on the new replica ID from the rollout result. Fix and retry, or `rollback_deploy`.
- **`rollback_deploy` fails** → `list_deploy_versions` to confirm a previous version exists. A first-ever deploy has nothing to roll back to.
- **Health check never passes for new replicas but old ones are fine** → likely an app-level regression in the new image (wrong port, missing env var, bad config). Check logs, not the platform.

## Example Session

> **User:** Update the API service to v2.3.0 without downtime.
>
> **Agent:**
> 1. `service_get` `{"name": "api"}` → 3 replicas, all healthy
> 2. `health_check_list` → confirms HTTP probe on :8000/health, all healthy
> 3. `deploy_rollout` `{"service_name": "api", "new_image": "registry.example.com/api:v2.3.0", "strategy": "rolling", "health_wait_seconds": 90, "abort_on_failure": true}`
>    → result: 3 steps, all `healthy`, `aborted: false`
> 4. `service_get` `{"name": "api"}` → 3 replicas on v2.3.0
>
> Rolled out with zero downtime. All 3 replicas passed their health gate.
>
> ---
>
> **User:** That rollout aborted on replica 2. Roll it back.
>
> **Agent:**
> 1. `rollback_deploy` `{"app": "api"}` → redeployed prior version
> 2. `list_deploy_versions` `{"app": "api"}` → confirms current = previous stable
> 3. `service_get` `{"name": "api"}` → replicas back on v2.2.1, all healthy
>
> Rolled back. The mixed state from the aborted rollout is resolved.
