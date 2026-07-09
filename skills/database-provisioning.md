# Skill: Database Provisioning

**Triggers:** When the user asks to create/provision/set up a database, add Postgres/MySQL/Redis/MongoDB, "give me a DB", back up a database, or wire an app to a new data store.
**Prerequisites:** A reachable cluster with enough capacity for the DB container + persistent volume. Admin role for `database_create`.
**RBAC Required:** admin (for `database_create`, `secret_set`); operator (for `secret_get`, `database_backup`)

## Workflow

1. **Provision the database** — `database_create` with `name`, `type` (postgres|mysql|redis|mongodb), `version`, `memory_mb`
   - This single tool creates the container, a persistent volume, a health check, and stores generated credentials as an encrypted secret. Returns the container ID and the secret name holding the password.
2. **Wait for health** — poll `health_check_status` with `container_id` until status is `healthy`
   - Databases take longer to accept connections than web apps (Postgres init, Mongo replica setup). Don't proceed to step 3 until healthy. Typical: 10–30s.
3. **Retrieve the credentials** — `secret_get` with the secret name returned by `database_create`
   - Returns the generated password. Handle carefully — this is the only time the plaintext is exposed to the operator.
4. **Create a connection string ConfigMap** — `configmap_create` with `name` (e.g. `pg-api-conn`), `data` containing the connection string built from the DB's host/port/user/password/dbname
   - Apps consume this as a non-sensitive config. The password itself stays in the secret; the ConfigMap can reference it by name or inline it depending on your app's pattern.

## Per-Type Defaults

`database_create` applies sane defaults per type. Know these when wiring apps:

| Type | Default Image | Port | Data Mount | Default User |
|------|---------------|------|------------|--------------|
| postgres | `postgres:16-alpine` | **5432** | `/var/lib/postgresql/data` | `postgres` |
| mysql | `mysql:8` | **3306** | `/var/lib/mysql` | `root` |
| redis | `redis:7-alpine` | **6379** | `/data` | (none; password in `REDIS_PASSWORD`) |
| mongodb | `mongo:7` | **27017** | `/data/db` | `root` |

Override `version` (e.g. `"15"` for older Postgres) and `memory_mb` (default 512; production DBs usually need more) as needed. The port is fixed per type — wire apps to the values above unless you've added a custom port mapping.

## Decision Points

- **Which type?** Match the app's requirement. If unspecified, Postgres is the safest general-purpose default.
- **Memory?** Default 512 MB is fine for dev. Production: 1024–4096+ depending on dataset. Set via `memory_mb` at creation; adjust later with `resource_set_limits`.
- **Need external access?** Add `add_port_mapping` (host_port → DB port) after creation. **Security note:** exposing a DB publicly is risky — prefer keeping it internal and having apps connect over the cluster network.
- **Multiple apps sharing one DB?** Provision once, then create separate credentials/databases inside it via `exec_in_container` (e.g. `CREATE DATABASE app2; CREATE USER app2 ...`). Don't spin up a second DB instance unless isolation is required.
- **Redis without auth?** Redis sets `REDIS_PASSWORD` env var; the app must use it. A passwordless Redis is only acceptable on a fully isolated network.

## Backup Workflow

1. **On-demand backup** — `database_backup` with the DB name → creates a volume backup with SHA256 integrity.
2. **Scheduled backup** — `job_create` with `name`, `schedule` (e.g. `"daily"`), `tool: "database_backup"`, `args: {"name": "<db-name>"}`. Runs automatically.
3. **Restore** — `database_restore` with the DB name and backup ID. Admin role. Verify with `health_check_status` after.
4. **List available backups** — `list_backups` filtered by the DB's volume.

## Error Recovery

- **`database_create` fails** → most commonly insufficient resources. `suggest_node` to check capacity, or lower `memory_mb`. Also check the image is pullable (`image_list`).
- **Health never goes green** → `get_container_logs` on the DB container. Common causes: wrong version tag, volume permission issues, invalid env vars. For Postgres, look for `FATAL: ...` lines.
- **App can't connect** → verify the port (use the table above), verify the password via `secret_get`, and check the DB container is healthy. Use `exec_in_container` to test connectivity from inside (`psql -U postgres -h localhost`).
- **`secret_get` returns "not found"** → the secret name was lost. Check `secret_list` for a name matching the DB; if truly gone, the DB will need reprovisioning (the password is also typically visible in the container's env via `get_container`).
- **Restore fails integrity check** → the backup is corrupt. `list_backups` for an earlier one. This is why regular backups matter.

## Example Session

> **User:** Set up a Postgres database for the API. Call it api-db.
>
> **Agent:**
> 1. `database_create` `{"name": "api-db", "type": "postgres", "version": "16", "memory_mb": 1024}`
>    → container_id `c-pg01`, secret `api-db-password`, port 5432
> 2. `health_check_status` `{"container_id": "c-pg01"}` → poll until `healthy` (~15s)
> 3. `secret_get` `{"name": "api-db-password"}` → password `s3cr3t-h4sh`
> 4. `configmap_create` `{"name": "api-db-conn", "data": {"DATABASE_URL": "postgresql://postgres:s3cr3t-h4sh@api-db:5432/api-db"}}`
> 5. (optional) `database_backup` `{"name": "api-db"}` → initial baseline backup
>
> Postgres is up on port 5432, healthy, password stored encrypted, and the API can read the connection string from the `api-db-conn` ConfigMap. Want a daily backup job?
