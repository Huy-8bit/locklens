# Storage and retention

## The monitoring store

`locklens server` keeps everything it collects in its own PostgreSQL 14+
database, the **monitoring store** (`LOCKLENS_STORE_DSN`). It must not be one
of the monitored databases: collectors are read-only, but the store is
written to continuously.

The store role needs to own the schema objects (it runs the migrations). No
extension is required.

### Schema and migrations

Migrations are SQL files in [backend/migrations/](../backend/migrations), embedded in the
binary and applied in order at startup (or with `locklens migrate`). They run
inside a transaction under a PostgreSQL advisory lock, so several instances
starting at once apply each migration exactly once. Applied versions are
recorded in `schema_migrations`; `/readyz` reports the schema version.

| Table | Content | Retention |
| --- | --- | --- |
| `users`, `auth_sessions` | Users with roles (bcrypt hashes) and server-side sessions (SHA-256 of the token only) | Sessions until expiry |
| `targets`, `target_status` | Targets with the **encrypted** DSN and display fields (host, port, database, user — no password); latest status | Soft-deleted targets are removed once their history and incidents are gone |
| `metric_points` | `(target, metric, label, ts, value)`; `value` is NULL when unknown | History |
| `query_identities`, `query_samples`, `statement_collections` | Query identities (text only with opt-in), per-collection statement deltas, collection metadata (interval, reset, evictions) | History |
| `activity_snapshots` | Full activity snapshots (sessions, blocking edges, waiting locks) | History |
| `table_stats`, `index_stats`, `state_snapshots` | Table/index statistics, replication and other state | History |
| `collection_gaps` | Missing-data intervals with reasons | Findings |
| `findings` | Rule findings with evidence and lifecycle | Findings (resolved ones) |
| `incidents`, `incident_events` | Incidents and their evidence | Incidents (resolved ones) |
| `settings` | Rule thresholds | — |

## Retention

| Data | Variable | Default |
| --- | --- | --- |
| History (metrics, statements, snapshots, table stats, state) | `LOCKLENS_RETENTION` | 7 days |
| Resolved incidents and their events | `LOCKLENS_INCIDENT_RETENTION` | 90 days |
| Resolved findings, collection gaps | `LOCKLENS_FINDING_RETENTION` | 30 days |

Incidents contain their own copy of the evidence, so expiring raw history
never changes an incident. Open incidents are never deleted.

Cleanup runs one minute after startup and then every hour. It deletes in batches of
5,000 rows with a 50 ms pause between batches, so it never holds long locks
on the store or blocks collectors and the API. Deleted space is reused by
PostgreSQL after autovacuum; the files do not shrink by themselves.

**Deleting a target** is a soft delete by default: it stops collection and
hides the target, and its history expires through normal retention. **Delete
with purge** removes its history immediately (in batches); incidents are kept
unless you also choose to delete them (they keep the target's name).

## Storage growth

Measured in the Docker lab (PostgreSQL 18.6 store; demo mode, so statements
were collected every 15 s instead of 60 s; scenarios running part of the
time) over 36 minutes:

| Target | Interval | Metric rows | Approx. growth |
| --- | --- | --- | --- |
| `lab-primary` | 5 s | ~300 per minute | ~3 MB per hour of metric points |
| `lab-standby` | 10 s | ~190 per minute | ~2 MB per hour of metric points |

All tables together grew by about 7 MB for both targets in that period
(`metric_points` 3.3 MB, `query_samples` 1.3 MB, the rest well under 1 MB
each). Extrapolated **for this lab workload only**, one target at a 5 s
interval stores on the order of 70–100 MB per day, so the default 7-day
retention needs roughly 0.5–0.7 GB per such target, plus indexes and free
space.

What drives growth on real systems:

- **Interval**: activity metrics scale linearly with 1/interval. 10 s halves
  the volume of 5 s.
- **Per-label series**: one series per database (`db.*`), replica, slot, and
  mount point.
- **Statements**: one row per changed statement per collection, capped at
  2,000 per collection. Busy servers with many distinct statements store
  more; query text (opt-in) is stored once per identity.
- **Blocking and long transactions**: a full activity snapshot is stored for
  every collection while either exists, otherwise every 5 minutes. Snapshots
  grow with the number of sessions.
- **Incidents**: at most 1,000 events or 5 MiB each.

A second measurement with **production intervals** (no demo mode), from
`scripts/perf-smoke.sh` with 20 targets at a 10 s interval for 5 minutes and
no lab scenario running, grew the store by 4.66 MiB, about **2.8 MiB per
target-hour** (roughly 65 MB per target-day, or 0.5 GB per target for the
default 7-day retention). The 5-minute window includes startup writes (the first table-statistics and
activity snapshots), so a longer run may give a lower hourly figure; that has
not been measured.

These figures come from a small local lab and are not a capacity guarantee.
Check the actual size on the **Settings** page (Monitoring store) or with
`GET /api/v1/storage`, and adjust intervals and retention.

## Store outages

When the store is unavailable, collection continues and batches wait in a
bounded in-memory queue (`LOCKLENS_WRITE_QUEUE`, default 2,048 batches). When
the queue is full, the oldest batch is dropped and counted
(`locklens_store_dropped_batches_total`); once the store is reachable again,
each loss is recorded as a collection gap. Memory use never grows without
bound, and lost data is visible rather than hidden. `/readyz` returns 503
while the store is unreachable.

## Backup and restore

The store is a normal PostgreSQL database:

```sh
pg_dump --format=custom --file=locklens-store.dump "$LOCKLENS_STORE_DSN"
pg_restore --clean --if-exists --no-owner --dbname="$LOCKLENS_STORE_DSN" locklens-store.dump
```

- Stop `locklens server` (or accept a slightly inconsistent dump) during
  restore.
- **Back up `LOCKLENS_MASTER_KEY` separately.** Target DSNs in the dump are
  encrypted with it; without the key they cannot be decrypted, and each
  target's connection string must be re-entered. The rest of the data stays
  readable.
- The dump contains query text only for targets that opted in, and no
  passwords in clear text (DSNs encrypted, admin password bcrypt-hashed).
  Treat it as sensitive anyway: it describes your databases and workload.
- After restoring an older dump, `locklens server` applies any newer
  migrations at startup.

For the Docker lab, the store lives in the `store-data` volume; `make down`
deletes it.

## Rotating the master key

There is no in-place re-encryption command yet. To change
`LOCKLENS_MASTER_KEY`, start with the new key and re-enter each target's DSN
(Settings → target → Connection string). Targets whose DSN cannot be decrypted
show the error "cannot decrypt stored secret (wrong LOCKLENS_MASTER_KEY or
corrupted data)" and stay unreachable until updated.
