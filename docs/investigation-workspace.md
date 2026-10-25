# Investigation workspace

LockLens combines asynchronous monitoring with explicit, interactive investigation.
Monitoring collectors never fetch application table rows. The investigation service
uses a separate, encrypted target DSN and opens a disposable connection per operation.
It does not connect to every database discovered on a server.

## Permissions and credentials

The current authentication model is a **single administrator**, not multi-user RBAC.
The administrator can configure four distinct capabilities per target:

| Capability | Default | Credential / policy boundary |
| --- | --- | --- |
| Monitoring | Existing authenticated access | Collector role; query-text collection is independently opt-in |
| Catalog metadata | Denied | Dedicated investigation role, fixed target host/port/database |
| Table data | Denied | Separate target capability; database SELECT grants still apply |
| SQL execution | Denied | Separate target capability; read-only transaction and database grants |

Configure access under **Data explorer → Access settings** (or the bootstrap file's
`workspace` block, see [configuration](configuration.md#bootstrap-targets)). The DSN is
write-only, encrypted with AES-GCM and target-specific associated data. A blank DSN
retains credentials. Changing the target endpoint requires reconfiguring investigation
credentials. Policy changes cancel outstanding operations. Credentials are never
returned in API responses, exports, application access logs, or audit records.

Use a separate login role with USAGE on intended schemas and SELECT only on intended
relations. Do not grant administrative memberships, ownership, CREATE, write privileges,
server-file access or execution of unsafe/security-definer functions. LockLens rejects
superuser, createdb, createrole, bypassrls and administrative/server-file memberships;
DBAs remain responsible for the SELECT/function grant surface, including PUBLIC grants.
On older installations check `CREATE ON SCHEMA public` privileges explicitly.
SQL can read any object granted to this dedicated role; the schema selector is a
search-path convenience, **not** an access-control boundary.

Raw SQL/results never enter the workspace audit table. Audit records contain target,
actor class, action, status/SQLSTATE, duration and row count; only the latest approximately
10,000 records are retained. This is bounded operational auditing, not a durable compliance
archive. Auditing failure withholds a successful operation's result. PostgreSQL's own log
configuration and independently enabled monitoring query-text collection are separate
policies; consider those when submitting sensitive literals or SQL comments.

## Data explorer

The web UI exposes catalog metadata and table data. SQL execution remains an
API capability (disabled by default); the UI has no SQL editor.

Navigation: target → configured database → schema → relation. Other database names can
be inspected, but switching connections is deliberately unavailable. Catalogs expose
columns/types/nullability/defaults, constraints, PK/FK definitions, indexes/usage/size,
partition parents/children, estimated row counts, sizes and vacuum timestamps.
Catalog responses are bounded to 200 rows per section and cached in the browser query
cache for 30 seconds. Refresh metadata requests current catalogs. System schemas are
excluded from the application data browser. No implicit `COUNT(*)` runs.

Opening a table's **Data** tab reads its first page. Each request has at most 100 rows (UI: 25, 50, or 100),
100 columns and a 1024-byte filter. Sort columns and selected columns must exist in the
catalog; identifiers are quoted, values are parameters. Filters support textual equality,
substring, NULL and NOT NULL. This can require a scan; statement timeouts still apply.

A selected, single-column primary key of integer/text/varchar/UUID type enables keyset
pagination when sorted by that key. Other sorts, composite keys and unsupported types
use bounded offset (maximum 10,000). Concurrent writes can change page membership;
there is no cross-request snapshot guarantee. NULL and empty strings remain distinct.
Opening a cell shows its full value (valid JSON pretty-printed) with a copy button.
No background data polling occurs. The UI queues workspace requests so that at most two
run per target, matching the server limit, and retries a busy (429) answer with backoff.

## SQL execution and limits

`POST /api/v1/targets/{id}/workspace/run` with `action: "sql"` (or `"explain"`)
executes one explicit statement when the target's SQL capability is enabled. The result
carries PostgreSQL column type OIDs, result rows, duration, truncation, SQLSTATE and
available error position. Generic error messages deliberately
omit PostgreSQL detail/hint fields, which can contain data values.

| Limit | Value |
| --- | --- |
| Statement text | 32 KiB |
| Connection timeout | 3 seconds |
| Statement / lock timeout | 15 seconds / 1 second |
| Overall operation deadline | 18 seconds |
| Concurrent connections | 2 per target; 8 globally; excess requests return 429 |
| SQL result | 500 rows; 100 columns; approximately 1 MiB serialized result (including JSON escaping) |
| Cell | 8192 bytes, UTF-8 safe truncation |
| Protocol message body | 1 MiB; an oversized single row fails safely |
| Table browser offset | 10,000 |

PostgreSQL parses the query through `DECLARE ... CURSOR FOR` using the extended protocol,
which accepts one statement. This is not a SELECT-prefix filter. Each operation begins
a read-only transaction; DML CTEs and row locks are rejected by PostgreSQL. Fetches are
bounded, serialized row sizes include JSON escaping, and the connection is closed after rollback. EXPLAIN uses JSON without ANALYZE;
the tree shows planner costs and estimated rows, not measured runtime. A very large JSON
plan can be truncated and remain visible as raw bounded text without a tree.

Read-only mode is **not an absolute SQL sandbox**. Functions, extensions, external
services, session advisory locks and planning-time evaluation require trusted grants.
Connection disposal releases session state/locks. Limits bound LockLens concurrency,
transport and results; they cannot pre-limit all target-side planner/executor memory or
CPU. Use PostgreSQL resource controls and a trusted role for production deployments.
See PostgreSQL's [read-only transaction semantics](https://www.postgresql.org/docs/18/sql-set-transaction.html)
and [cursor query grammar](https://www.postgresql.org/docs/18/sql-declare.html).

Cancellation is keyed by a random request ID, authenticated session and target, and
cancels only an active operation owned by that session. There is no generic backend
terminate endpoint. Navigating away aborts the HTTP request and its query context.

The policy's `history_enabled` flag is kept for API compatibility; the current UI stores
no statements or results in the browser.

## Metrics

The **All instances** page shows each target's latest samples; per-instance pages show
history. Percentages, lag and host metrics are never summed across targets. Duplicate
configured targets can double-count a server. Freshness thresholds are three collector
intervals, with a small minimum grace period. Missing data never becomes zero.

| Measurement | Source / scope | Meaning and limits |
| --- | --- | --- |
| Transactions | pg_stat_database, all databases | Commit / rollback counter deltas per actual elapsed second |
| Logical tuple operations | pg_stat_database, all databases | Tuples returned/fetched/inserted/updated/deleted; not SELECT or HTTP request counts |
| Buffer hits/reads | pg_stat_database, all databases | Shared-buffer hits and block reads; OS cache can satisfy reads, so not physical disk I/O |
| PostgreSQL I/O | pg_stat_io, PG 16+ | Operation counts summed across backend/context/object; version-dependent availability |
| Query execution ms/s | pg_stat_statements | Accumulated observed execution time, includes waiting; not CPU or utilization percent |
| WAL | LSN differences | Primary generated bytes versus standby received bytes, kept separate |
| Temp files / deadlocks | pg_stat_database | Bytes per second / event deltas; counter resets produce unknown intervals |
| Archiver failures | pg_stat_archiver | Failure deltas; an inactive archiver can legitimately remain zero |
| Host CPU/RAM/disk I/O | Optional node_exporter | Unavailable without exporter; never inferred from SQL stats |
| Database growth | Catalog database sizes | Slow collection cadence, latest-value aggregation |

Counters use deltas, rates use elapsed-time weighted means, pressure and lag use maxima,
and missing or partial buckets remain gaps. The default range is 15 minutes. See the
[UI guide](ui-design.md#reading-the-charts).

## Topology and live activity

Topology mappings (`GET/PUT/DELETE /api/v1/topology`) record which standby belongs to which
primary, with the replication `application_name` and provenance. The current UI does not
draw a topology view; the Replication page shows each server's own observations.

Live activity uses the existing bounded monitoring snapshot: client sessions with open
transactions plus blocking participants, not all idle pooled connections. Query text is
present only under the collection policy and PostgreSQL visibility grants. Filters cover
database/application/user/state; query age, transaction age, wait and blockers remain
distinct. This is sampling, not tracing. The page explicitly shows
latest observation time and interval; historical chart selection does not imply historical
session replay. Use incident blocking snapshots for historical blocker → waiter evidence.
