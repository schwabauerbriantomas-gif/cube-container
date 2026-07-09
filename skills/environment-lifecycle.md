# Skill: Environment Lifecycle

**Triggers:** When the user asks to set up dev/staging/prod environments, promote an app between environments, manage environments, isolate dev from prod, protect production, or roll back a specific environment.
**Prerequisites:** A running cluster. The default environments (`dev`, `staging`, `prod`) are auto-created; additional environments need explicit creation.
**RBAC Required:** admin (for `env_create`); operator (for `env_promote`)

## Workflow

1. **Ensure environments exist** — `env_list` to see current environments and container counts
   - `dev`, `staging`, `prod` exist by default. Create custom ones (e.g. `qa`, `hotfix`) with `env_create`.
2. **Create additional environments (optional)** — `env_create` with `name`, `description`, `protected` (bool)
   - Use `protected: true` for production-like environments that shouldn't be accidentally destroyed. Promotion INTO a protected environment is allowed; promotion OUT of or deletion of a protected environment should be guarded by policy.
3. **Deploy to an environment** — deploy into `dev` first (via `deploy_from_git` / `create_container` / `deploy_from_code`), test there, then promote.
4. **Promote between environments** — `env_promote` with `source_env`, `target_env`, `container_id`
   - Creates a new container in the target environment with the same image, then removes the source. The standard flow is `dev` → `staging` → `prod`.

## When to Promote vs. Deploy Fresh

This is the core decision in environment management:

- **`env_promote`** — Use when you want the **exact same artifact** (image + config) to move up the pipeline. This guarantees what you tested in staging is what runs in prod. Promotion creates a new container in the target env with the same image and removes the source. **This is the recommended path for production changes** — it preserves artifact immutability.
- **`deploy_from_git` to each env** — Use when environments need **different configurations** from the same source (e.g., dev pulls `main`, staging pulls a release branch, prod pulls a tag). Each env gets a fresh build. Risk: the artifact isn't identical across envs — a staging-tested image might differ from what prod builds.

**Rule of thumb:** For production safety, prefer promote (dev → staging → prod of the same container/image). Use per-env `deploy_from_git` only when envs genuinely diverge (different branches, different configs) and you accept the immutability trade-off.

## Protected Environments

- Mark `prod` (and any production-like env) as `protected: true` at creation or via recreation.
- Promotion INTO a protected environment works normally — this is how prod gets updates.
- The protection guards against accidental deletion or destructive operations. Treat protected envs as requiring explicit confirmation for any teardown.
- `env_promote` out of a protected environment (e.g., prod → dev for a hotfix rollback) should be done deliberately and is a policy decision, not a technical block.

## Rollback Per Environment

Each environment has its own deployment version history. To roll back:

1. Identify the environment and the app/container affected.
2. `list_deploy_versions` for that app to see version history.
3. `rollback_deploy` with the app identifier — redeploys the prior version **in the current environment context**. (Admin role.)
4. Verify with `health_check_status` and `env_get` on the affected environment.

**Important:** Rolling back `prod` does not affect `staging` or `dev`. Environments are isolated. If a bad change reached prod via promotion, rolling back prod leaves staging/dev on the (still bad) version — you'll need to address those environments separately, or re-promote once the fix is ready.

## Decision Points

- **Need a new environment?** `env_create` with a descriptive name. Common additions: `qa`, `hotfix`, `preview` (for PR-based review environments).
- **Promote or redeploy?** Promote for identical-artifact pipeline (recommended for prod). Redeploy from git per-env when configs/branches diverge.
- **Promotion failed?** `env_promote` creates the target container then removes the source. If creation fails, the source is untouched. Check `get_container_logs` on the failed target, fix (image pull? resources?), and retry.
- **Different env, different config?** Use ConfigMaps per environment (`configmap_create` with env-scoped names like `api-config-dev`, `api-config-prod`). The image stays the same; only config differs. This pairs well with promotion.
- **Need to tear down an environment?** `env_get` to see what's in it. Remove containers first (`kill_container`), then the environment can be cleaned up. For protected environments, confirm explicitly first.

## Error Recovery

- **`env_promote` fails to create the target container** → usually an image pull failure or insufficient resources on the target. The source container is preserved (promotion is create-then-remove). Fix the blocker and retry.
- **`env_promote` removed the source but target is unhealthy** → the target container exists but the app isn't serving. `get_container_logs` on the target. If it's a config issue, fix via `configmap_update` or `exec_in_container`. The source is gone — you may need to redeploy it in the source env if the target can't be salvaged.
- **`env_create` fails** → name collision (use `env_list` to check) or invalid name. Names should be lowercase, no spaces.
- **Promoted container can't reach dependencies** → environments are isolated. A prod container can't reach a dev DB by default. Either promote the dependency too, or point the prod container at prod-scoped dependencies via ConfigMap. This is a common promotion pitfall.
- **`rollback_deploy` finds no prior version** → the env has only ever had one deploy. Nothing to roll back to. Redeploy a known-good image instead.

## Example Session

> **User:** Set up dev, staging, and prod for the API. Promote the current dev build to staging.
>
> **Agent:**
> 1. `env_list` → `dev` (1 container), `staging` (0), `prod` (0) — defaults exist
> 2. `env_get` `{"name": "dev"}` → container `c-dev-api` (image `api:v2.3.0`)
> 3. `env_promote` `{"source_env": "dev", "target_env": "staging", "container_id": "c-dev-api"}`
>    → new container `c-stg-api` in staging (same image), `c-dev-api` removed from dev
> 4. `health_check_status` `{"container_id": "c-stg-api"}` → healthy
> 5. `env_get` `{"name": "staging"}` → confirms `c-stg-api` present
>
> Promoted to staging. Dev is now empty (promotion removes the source) — you'll want to redeploy to dev for ongoing development. Ready to promote staging → prod after QA?
>
> ---
>
> **User:** Production is broken. Roll back prod only.
>
> **Agent:**
> 1. `list_deploy_versions` `{"app": "api"}` → current v2.3.0, previous v2.2.1
> 2. `rollback_deploy` `{"app": "api"}` → prod redeployed v2.2.1 (admin)
> 3. `health_check_status` on the prod container → healthy
> 4. `env_get` `{"name": "prod"}` → confirms rollback applied
>
> Prod rolled back to v2.2.1. Staging and dev are unaffected (still on v2.3.0) — address those once the v2.3.0 issue is fixed.
