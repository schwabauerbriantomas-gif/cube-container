# Skill: Backup Strategy

**Triggers:** When the user asks to back up, set up backups, schedule backups, create a restore point, restore from backup, clean up / reclaim disk space, prune images/volumes, or "make sure we can recover".
**Prerequisites:** Volumes or containers worth backing up, and enough local disk space for backup files. For scheduled backups, decide on a schedule and retention approach.
**RBAC Required:** operator (for `backup_volume`, `backup_container`, `gc_prune_*`, `database_backup`); admin (for `restore_backup`, `delete_backup`, `job_create`, `rollback_deploy`)

## Workflow — Create Backups

1. **Back up a volume** — `backup_volume` with `volume_id`
   - Creates a tar.gz with SHA256 integrity check. Use for persistent data (DB volumes, app data volumes).
2. **Back up a full container** — `backup_container` with `container_id`
   - Captures the config manifest + all mounted volumes. Enables full recovery on restore. Heavier than a volume backup; use for complete service snapshots.
3. **Schedule recurring backups** — `job_create` with `name`, `schedule` (e.g. `"daily"`, `"every 6h"`), `tool` (`backup_volume` or `backup_container` or `database_backup`), `args` (the tool's args object)
   - Automated backups run on schedule. Pair with a GC strategy (below) so old backups don't fill the disk.
4. **Verify backups exist** — `list_backups` to see all backups with size, checksum, and restorable status.

## Workflow — Restore

5. **Identify the backup** — `list_backups`, find the one matching the lost volume/container, note its ID.
6. **Restore** — `restore_backup` with `backup_id` (admin)
   - Verifies SHA256 integrity before restoring. For containers, recreates the container from the manifest. For volumes, restores the files.
7. **Verify** — `health_check_status` on the restored container; `volume_info` on the restored volume.
8. **Rollback the deploy (if needed)** — if the restore is part of recovering from a bad deploy, `rollback_deploy` (admin) to get the code back to the last good version, then ensure the restored volume is attached.

## Per-Backup-Type Guidance

- **`backup_volume`** — Use for data volumes (DBs, uploads, app state). Fast, file-level. Run frequently (daily or more).
- **`backup_container`** — Use for complete service snapshots (config + data). Run weekly or before risky changes. Heavier.
- **`database_backup`** — Use for managed DBs (Postgres/MySQL/Redis/Mongo). Specifically targets the DB's volume with DB-aware handling. Prefer this over raw `backup_volume` for DBs.

## Garbage Collection Strategy

To reclaim disk space (critical on edge nodes with limited storage):

9. **Check disk usage** — `gc_disk_usage` to see the breakdown: images, containers, volumes — total, active, reclaimable.
10. **Prune unused images** — `gc_prune_images` — removes unused/dangling images older than 7 days. Safe; only touches images not referenced by any container.
11. **Prune orphaned volumes** — `gc_prune_volumes` — removes volumes not attached to any running container, including deploy-managed volumes with no active container. **Review `list_volumes` first** — an orphaned volume might be intentionally detached (e.g., a volume waiting for a container to come back). Back up before pruning if unsure.
12. **Schedule GC** — `job_create` with `tool: "gc_prune_images"`, weekly schedule. Keeps disk usage bounded automatically.

## Decision Points

- **How often to back up?** Data loss tolerance drives this. DBs with frequent writes → daily or hourly. Static assets → weekly. Use `job_create` to automate.
- **Volume vs. container backup?** If you only need the data → `backup_volume`. If you need to reconstruct the entire service (env vars, ports, config) → `backup_container`.
- **Restore vs. rollback?** Restore (`restore_backup`) recovers **data** from a backup. Rollback (`rollback_deploy`) reverts **code/version** to a prior deploy. A full recovery from a bad deploy often needs BOTH: rollback the code, then restore the data if it was corrupted.
- **Prune aggressively?** `gc_prune_volumes` is destructive — orphaned volumes are deleted. If a container is temporarily stopped (not removed), its volume is still attached and safe. If the container was killed, the volume becomes orphaned and is a prune candidate. When in doubt, `backup_volume` before pruning.
- **Where are backups stored?** Locally on the node. For off-site safety, the backup files (tar.gz) should be copied to external storage out-of-band. The platform doesn't do remote backup replication automatically.

## Error Recovery

- **`restore_backup` fails integrity check** → the backup file is corrupt (disk error, incomplete write). `list_backups` for an earlier backup. This is why you keep multiple backup generations.
- **`backup_volume` fails** → usually disk full. Run `gc_prune_images` + `gc_prune_volumes` to reclaim space, then retry. Also check `gc_disk_usage`.
- **Restored container won't start** → the manifest may reference an image/template that's been pruned. `image_pull` the image, or `image_build` from source, then retry. `get_container_logs` for the specific error.
- **`gc_prune_volumes` deleted something needed** → it only removes orphaned (unattached) volumes. If a needed volume was pruned, it means the container was killed and the volume sat orphaned. Recovery is from the most recent `backup_volume` of that volume. Lesson: back up before killing containers you intend to recreate.
- **Scheduled backup job not running** → `job_list` to check next-run time and status. Jobs are recorded as intent; verify the job scheduler is operational. `job_run` to trigger it manually as a test.

## Example Session

> **User:** Set up daily DB backups and clean up old images. We're at 85% disk.
>
> **Agent:**
> 1. `gc_disk_usage` → 85% used; 12GB reclaimable in images, 3GB in orphaned volumes
> 2. `list_volumes` → 2 orphaned volumes (`old-cache`, `stale-uploads`) — confirm with user before pruning
> 3. `gc_prune_images` → reclaimed 11.8GB (kept images <7 days old)
> 4. (user confirms) `gc_prune_volumes` → removed `old-cache`, `stale-uploads` (reclaimed 3GB)
> 5. `backup_volume` `{"volume_id": "api-db-data"}` → initial backup, SHA256 verified ✓
> 6. `job_create` `{"name": "daily-db-backup", "schedule": "daily", "tool": "database_backup", "args": {"name": "api-db"}}` → job scheduled for 00:00 daily
> 7. `job_create` `{"name": "weekly-image-prune", "schedule": "weekly", "tool": "gc_prune_images", "args": {}}` → GC scheduled
> 8. `list_backups` → confirms initial backup present
>
> Disk down to 38%. Daily DB backups and weekly image GC are scheduled. Want a volume backup job for the app data too?
>
> ---
>
> **User:** Restore the DB from yesterday's backup — today's deploy corrupted it.
>
> **Agent:**
> 1. `list_backups` → find the DB backup from yesterday (`bk-7c3`)
> 2. `rollback_deploy` `{"app": "api"}` → revert code to prior version (admin)
> 3. `restore_backup` `{"backup_id": "bk-7c3"}` → integrity verified, data restored (admin)
> 4. `health_check_status` `{"container_id": "c-pg01"}` → DB healthy
> 5. `exec_in_container` `{"container_id": "c-pg01", "command": "psql -U postgres -c 'SELECT count(*) FROM users'"}` → data intact
>
> Recovered: code rolled back, DB data restored from backup, integrity verified.
