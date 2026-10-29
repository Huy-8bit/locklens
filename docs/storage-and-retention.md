# Storage and retention

## The monitoring store

`locklens server` keeps everything it collects in its own PostgreSQL 14+
database, the **monitoring store** (`LOCKLENS_STORE_DSN`). It must not be one
of the monitored databases: collectors are read-only, but the store is
written to continuously.

The store role needs to own the schema objects (it runs the migrations). No
extension is required; with **TimescaleDB** (the default in Docker Compose)
history is partitioned by time, compressed, and expired by whole chunks (see
[TimescaleDB](#timescaledb)).

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
| `query_rollups`, `statement_coverage` | Five-minute statement rollups and collection coverage per bucket (with resets), the source of query baselines | Baselines |
| `metric_rollups_1m`, `metric_rollups_1h` | Downsampled metrics: per bucket the sample counts (known and all), sum, max, last value, and time-weighted sums, so every aggregation (and "unknown") is the same as from raw points | Minute / hour rollups |
| `catalog_state` | The latest schema catalog of each target (indexes, tables, foreign keys, column statistics without data values) | Replaced on each collection |
| `recommendations` | Optimizer recommendations with evidence, SQL, decisions, application, before/after measurement, and what-if results | Findings (resolved and dismissed ones) |
| `query_plans` | Execution plans saved on request (EXPLAIN output, shape fingerprint, cost, who and how) | Incidents |
| `activity_snapshots` | Full activity snapshots (sessions, blocking edges, waiting locks) | History |
| `table_stats`, `index_stats`, `state_snapshots` | Table/index statistics, replication and other state | History |
| `collection_gaps` | Missing-data intervals with reasons | Findings |
| `findings` | Rule findings with evidence and lifecycle | Findings (resolved ones) |
| `incidents`, `incident_events` | Incidents and their evidence | Incidents (resolved ones) |
| `settings` | Rule thresholds | — |

## TimescaleDB

With TimescaleDB, the history tables (`metric_points`, `query_samples`,
`statement_collections`, `activity_snapshots`, `table_stats`, `index_stats`,
`state_snapshots`, `load_samples`, `load_buckets`) and the baseline rollups
(`query_rollups`, weekly chunks; `statement_coverage`, 30-day chunks) and the
metric rollups (`metric_rollups_1m`, weekly; `metric_rollups_1h`, 30-day) are
**hypertables**:

- **Partitioned by time** into daily chunks (weekly for the small tables), so
  a chart reads only the chunks of its time range.
- **Compressed** (Community edition) once a chunk is older than
  `LOCKLENS_STORE_COMPRESS_AFTER` (default 24 h), by a TimescaleDB background
  job. Rows are grouped by target and metric (or statement), so a chart still
  reads one series, not the whole chunk. Late rows and `ON CONFLICT DO NOTHING`
  inserts into compressed chunks work.
- **Expired by whole chunks**: retention drops chunks older than the cutoff,
  which takes milliseconds and returns the disk space at once. Rows of the
  chunk that spans the cutoff stay until that chunk ends, at most one chunk
  interval longer than `LOCKLENS_RETENTION`.

The other tables (targets, incidents, findings, users, …) stay ordinary
tables.

| `LOCKLENS_STORE_TIMESCALE` | Behaviour |
| --- | --- |
| `auto` (default) | Use TimescaleDB when the store's server has the extension and preloads it; otherwise keep plain tables and say why on the Settings page and in the log |
| `on` | Refuse to start without TimescaleDB |
| `off` | Plain tables |

The setup runs after the migrations on every start and is idempotent. A store
that gains the extension later (for example after switching the Compose image)
is converted on the next start: existing rows are moved into chunks in one
transaction per table, which takes a while on a large store, once. Back up the
store first.

**Docker Compose** runs `timescale/timescaledb:2.30.2-pg18`: PostgreSQL 18.6
with the same data directory and C library as the `postgres:18.6-alpine` image
used before, so an existing store volume is converted in place. The extension
is preloaded with `shared_preload_libraries=timescaledb` on the command line,
and TimescaleDB's telemetry is turned off with
`timescaledb.telemetry_level=off`.

**Your own store server**: install TimescaleDB, add it to
`shared_preload_libraries`, restart, and run `CREATE EXTENSION timescaledb` once
as a superuser in the store database (LockLens tries this itself and falls back
to plain tables when its role may not). Managed PostgreSQL without TimescaleDB
works unchanged with `auto`.

**Editions.** Hypertables and chunk retention are in TimescaleDB's
Apache-2.0 edition; compression needs the Community edition (Timescale
License), which the `timescale/timescaledb` images contain. With an
Apache-only build (`-oss` images), LockLens uses hypertables without
compression.

### Measured

One synthetic `metric_points` table on the same machine: 7 days of 5 targets ×
40 metrics every 10 s (12.1 million rows), a mix of steady gauges, noisy
rates, and constants; PostgreSQL 18.6 with and without TimescaleDB 2.30.2, the
5 oldest days compressed.

| | PostgreSQL | TimescaleDB |
| --- | --- | --- |
| Size | 1,788 MB | 717 MB (compressed days: 1,099 MB → 33 MB, 33×; the 2 recent days stay uncompressed) |
| 7-day chart of one metric (15-minute steps), warm | ~155 ms | ~22 ms |
| Last hour of one metric (10-second steps), warm | ~1.5 ms | ~0.5 ms |
| Expire one day (1.7 million rows) | 534 ms as one `DELETE` (LockLens deletes in paced batches), space reused only after vacuum | 10 ms (`drop_chunks`), space returned at once |

Synthetic data, one machine: real metrics compress differently (steady
gauges better, noisy rates worse). Check the Settings page (Monitoring store),
which shows each table's size and compression ratio.

## Resolution of history

Raw metric points are kept for `LOCKLENS_RETENTION` at the collection
interval (10–15 s by default). Every minute LockLens rolls them up into
one-minute and one-hour buckets (`metric_rollups_1m`, `metric_rollups_1h`),
recomputing the last few minutes so late points are included. A chart reads:

- raw points for steps under 5 minutes over the raw history;
- one-minute rollups for steps of 5 minutes to 3 hours, and for ranges older
  than the raw history;
- hourly rollups for steps of 3 hours or more.

The newest minutes not rolled up yet are read from raw points and merged, so
a chart is never behind. Statement charts do the same with the five-minute
statement rollups for steps of 5 minutes or more. Short relative ranges
("15m", "1h") refresh after every collection in the UI (live tail).

## Retention

| Data | Variable | Default |
| --- | --- | --- |
| History (metrics, statements, snapshots, table stats, state) | `LOCKLENS_RETENTION` | 7 days |
| Resolved incidents and their events | `LOCKLENS_INCIDENT_RETENTION` | 90 days |
| Resolved findings, collection gaps | `LOCKLENS_FINDING_RETENTION` | 30 days |
| One-minute metric rollups | `LOCKLENS_MINUTE_ROLLUP_RETENTION` | 14 days |
| Hourly metric rollups | `LOCKLENS_HOUR_ROLLUP_RETENTION` | 90 days |
| Resolved and dismissed recommendations | `LOCKLENS_FINDING_RETENTION` | 30 days |
| Statement rollups and coverage (baselines) | `LOCKLENS_BASELINE_RETENTION` (never shorter than history) | 15 days |
| Saved execution plans | `LOCKLENS_INCIDENT_RETENTION` | 90 days |

Query identities stay while samples, rollups, or saved plans refer to them.

Incidents contain their own copy of the evidence, so expiring raw history
never changes an incident. Open incidents are never deleted.

Cleanup runs one minute after startup and then every hour. With TimescaleDB
it drops whole chunks of the history tables (see above). Otherwise, and for the
other tables, it deletes in batches of
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

With `make up`, the store lives in the `locklens_store` volume (`make down`
keeps it). For the demo lab, it lives in `locklens-lab_store`; `make lab-down`
deletes it.

## Rotating the master key

There is no in-place re-encryption command yet. To change
`LOCKLENS_MASTER_KEY`, start with the new key and re-enter each target's DSN
(Settings → Instances → Edit → Connection string). Targets whose DSN cannot be decrypted
show the error "cannot decrypt stored secret (wrong LOCKLENS_MASTER_KEY or
corrupted data)" and stay unreachable until updated.
