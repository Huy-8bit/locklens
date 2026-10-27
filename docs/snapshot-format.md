# Snapshot format

`locklens snapshot save` writes the raw observation as JSON. `snapshot show`,
`inspect --format json`, and `watch --format json` embed the same object under
the `snapshot` key of a report. The Go types are in
[`backend/internal/domain/model/snapshot.go`](../backend/internal/domain/model/snapshot.go); this document
describes the JSON they produce.

## Conventions

- Encoding: UTF-8 JSON, indented with two spaces in files.
- **Timestamps**: RFC 3339 strings in UTC with up to microsecond precision
  (`"2026-10-24T09:16:21.181638Z"`), as PostgreSQL stores them.
- **Nullable fields** are always present and set to `null` when unknown, except
  `query` and `query_truncated`, which are omitted when query text was not
  collected. A field is `null` when PostgreSQL returns NULL: by definition
  (`xact_start` outside a transaction, `waitstart` for a moment after a wait
  begins) or because the observing role may not see another role's session
  details (`details_visible: false`).
- **Lists** are always arrays, never `null`. Sessions are sorted by `pid`,
  edges by `(blocker, waiter)`.
- Raw data only: ages, counts, and roots are **not** stored. They are derived
  when the snapshot is shown, so an old file is re-analysed by the current
  rules.

## Top-level object

| Field | Type | Description |
| --- | --- | --- |
| `schema_version` | integer | Format version, currently `1` |
| `collected_at` | timestamp | Client clock (UTC) when collection finished |
| `server_time` | timestamp | Server `clock_timestamp()` after every source was read. All ages are measured against it |
| `tool` | object | `name` and `version` of the LockLens build |
| `collection` | object | See below |
| `server` | object | See below. Never contains credentials |
| `sessions` | array | Sessions involved in blocking plus client backends with an open transaction; LockLens' own connections excluded |
| `edges` | array | `{"blocker": int, "waiter": int}` from `pg_blocking_pids()`, deduplicated. `blocker: 0` is a prepared transaction |
| `prepared_transactions` | array | All rows of `pg_prepared_xacts` |
| `warnings` | array | `{"code": string, "message": string}` raised during collection |

### `collection`

| Field | Type | Description |
| --- | --- | --- |
| `duration_ms` | integer | Collection time on the client |
| `include_query` | boolean | Whether query text was collected |
| `query_max_length` | integer | Character limit applied to query text |
| `min_tx_age_seconds` | number | Long-transaction threshold at collection time; default for `snapshot show` |
| `atomic` | boolean | Always `false`: the snapshot is assembled from several reads |

### `server`

| Field | Type | Description |
| --- | --- | --- |
| `host`, `port`, `database`, `user` | string, integer, string, string | Where LockLens connected and as whom. `database` is `current_database()` |
| `version`, `version_num` | string, integer | `server_version`, `server_version_num` |
| `read_all_stats` | boolean | The role is a superuser or has the privileges of `pg_read_all_stats` |
| `track_activities` | boolean | Value of `track_activities` |

### `sessions[]`

| Field | Type | Nullable | Source / meaning |
| --- | --- | --- | --- |
| `pid` | integer | no | Backend PID |
| `leader_pid` | integer | yes | Parallel group leader (NULL for non-workers) |
| `database` | string | yes | `datname` |
| `user` | string | yes | `usename` |
| `application_name` | string | yes | Client-chosen, untrusted text |
| `client_addr` | string | yes | Client IP; NULL for Unix sockets or hidden sessions |
| `backend_type` | string | yes | e.g. `client backend` |
| `state` | string | yes | e.g. `active`, `idle in transaction` |
| `wait_event_type`, `wait_event` | string | yes | Current wait |
| `backend_start` | timestamp | yes | Connection start |
| `xact_start` | timestamp | yes | Transaction start |
| `query_start` | timestamp | yes | Current or last query start. Not the lock wait start |
| `state_change` | timestamp | yes | Last state change. Not the lock wait start |
| `backend_xid` | string | yes | Top-level transaction ID |
| `query` | string | omitted | Only with `include_query: true` and when visible; at most `query_max_length` runes |
| `query_truncated` | boolean | omitted | Present and `true` when `query` was cut |
| `query_id` | string (64-bit integer) | omitted | `pg_stat_activity.query_id` (PostgreSQL 14+): the top-level statement's `pg_stat_statements` queryid, when PostgreSQL computes it (`compute_query_id`). Identifies the statement without its text. Older snapshots do not have it |
| `details_visible` | boolean | no | `false` when PostgreSQL hides this session's details from the observer |
| `parallel_workers` | integer | no | Parallel workers led by this session |
| `waiting_locks` | array | no | Ungranted lock requests, below |

### `sessions[].waiting_locks[]`

| Field | Type | Nullable | Meaning |
| --- | --- | --- | --- |
| `pid` | integer | no | Backend that requested the lock (a parallel worker's own PID if applicable) |
| `locktype`, `mode` | string | no | `pg_locks.locktype`, `pg_locks.mode` |
| `database` | string | yes | Database name of the target |
| `relation_oid` | integer | yes | Relation OID |
| `relation` | string | yes | Relation name; resolved only for the current database and shared catalogs |
| `page`, `tuple` | integer | yes | Page and tuple number |
| `virtualxid`, `transactionid` | string | yes | Target transaction IDs |
| `classid`, `objid`, `objsubid` | integer | yes | Generic object identifiers (advisory and object locks) |
| `wait_start` | timestamp | yes | `pg_locks.waitstart` |

### `prepared_transactions[]`

`gid` (string), `transaction` (string), `prepared` (timestamp), `owner`
(string), `database` (string).

### Warning codes

| Code | Raised by | Meaning |
| --- | --- | --- |
| `restricted_visibility` | collection | The role lacks `pg_read_all_stats`; some details are hidden |
| `track_activities_off` | collection | Session state and query text are not maintained |
| `locklens_session_in_graph` | collection | A LockLens connection appeared as a blocker and was left out |
| `missing_session` | analysis | An edge references a PID without a `pg_stat_activity` row |
| `prepared_transaction_blocker` | analysis | PID 0 blocks someone |
| `cycle_detected` | analysis | A cycle exists in the observed edges; not a confirmed deadlock |

## Example

A real file saved by `locklens snapshot save --out docs/examples/snapshot-chain.json --min-tx-age 1s`
during the chain demo (also stored as
[examples/snapshot-chain.json](examples/snapshot-chain.json)). A unit test
checks that this block parses with unknown fields disallowed and is identical
to the stored file.

<!-- example:snapshot -->
```json
{
  "schema_version": 1,
  "collected_at": "2026-10-24T09:16:24.806041Z",
  "server_time": "2026-10-24T09:16:24.785069Z",
  "tool": {
    "name": "locklens",
    "version": "787665e"
  },
  "collection": {
    "duration_ms": 9,
    "include_query": false,
    "query_max_length": 200,
    "min_tx_age_seconds": 1,
    "atomic": false
  },
  "server": {
    "host": "127.0.0.1",
    "port": 55432,
    "database": "locklens_demo",
    "user": "locklens_observer",
    "version": "17.11",
    "version_num": 170011,
    "read_all_stats": true,
    "track_activities": true
  },
  "sessions": [
    {
      "pid": 116,
      "leader_pid": null,
      "database": "locklens_demo",
      "user": "demo_app",
      "application_name": "locklens-demo-A",
      "client_addr": "192.168.158.1",
      "backend_type": "client backend",
      "state": "idle in transaction",
      "wait_event_type": "Client",
      "wait_event": "ClientRead",
      "backend_start": "2026-10-24T09:16:21.17809Z",
      "xact_start": "2026-10-24T09:16:21.181638Z",
      "query_start": "2026-10-24T09:16:21.18412Z",
      "state_change": "2026-10-24T09:16:21.184229Z",
      "backend_xid": "746",
      "details_visible": true,
      "parallel_workers": 0,
      "waiting_locks": []
    },
    {
      "pid": 117,
      "leader_pid": null,
      "database": "locklens_demo",
      "user": "demo_app",
      "application_name": "locklens-demo-B",
      "client_addr": "192.168.158.1",
      "backend_type": "client backend",
      "state": "active",
      "wait_event_type": "Lock",
      "wait_event": "transactionid",
      "backend_start": "2026-10-24T09:16:21.185288Z",
      "xact_start": "2026-10-24T09:16:21.187978Z",
      "query_start": "2026-10-24T09:16:21.190189Z",
      "state_change": "2026-10-24T09:16:21.190189Z",
      "backend_xid": "747",
      "details_visible": true,
      "parallel_workers": 0,
      "waiting_locks": [
        {
          "pid": 117,
          "locktype": "transactionid",
          "mode": "ShareLock",
          "database": null,
          "relation_oid": null,
          "relation": null,
          "page": null,
          "tuple": null,
          "virtualxid": null,
          "transactionid": "746",
          "classid": null,
          "objid": null,
          "objsubid": null,
          "wait_start": "2026-10-24T09:16:21.19024Z"
        }
      ]
    },
    {
      "pid": 118,
      "leader_pid": null,
      "database": "locklens_demo",
      "user": "demo_app",
      "application_name": "locklens-demo-C",
      "client_addr": "192.168.158.1",
      "backend_type": "client backend",
      "state": "active",
      "wait_event_type": "Lock",
      "wait_event": "transactionid",
      "backend_start": "2026-10-24T09:16:21.191339Z",
      "xact_start": "2026-10-24T09:16:21.193954Z",
      "query_start": "2026-10-24T09:16:21.195818Z",
      "state_change": "2026-10-24T09:16:21.195818Z",
      "backend_xid": "748",
      "details_visible": true,
      "parallel_workers": 0,
      "waiting_locks": [
        {
          "pid": 118,
          "locktype": "transactionid",
          "mode": "ShareLock",
          "database": null,
          "relation_oid": null,
          "relation": null,
          "page": null,
          "tuple": null,
          "virtualxid": null,
          "transactionid": "747",
          "classid": null,
          "objid": null,
          "objsubid": null,
          "wait_start": "2026-10-24T09:16:21.19587Z"
        }
      ]
    }
  ],
  "edges": [
    {
      "blocker": 116,
      "waiter": 117
    },
    {
      "blocker": 117,
      "waiter": 118
    }
  ],
  "prepared_transactions": [],
  "warnings": []
}
```

## Report JSON (`analysis`)

`inspect`, `watch`, and `snapshot show` with `--format json` print
`{"snapshot": <the object above>, "analysis": {...}}`. The analysis is derived
and not stored in snapshot files.

| Field | Description |
| --- | --- |
| `min_tx_age_seconds` | Threshold used for `long_transactions` |
| `summary` | `blocked_sessions`, `blocking_sessions`, `root_blockers`, `cyclic_components`, `long_transactions`, `idle_in_transaction` |
| `nodes[]` | `id`, `kind` (`session`, `prepared_transaction`, `missing`), `blocked_by`, `blocks`, `direct_blocked_count`, `transitive_blocked_count`, `root`, `in_cycle`, `component` |
| `edges[]` | Deduplicated `{"blocker", "waiter"}` |
| `components[]` | `id`, `members`, `roots` (empty for cyclic components), `cyclic`, `cycle_members`, `entries` |
| `sessions[]` | `pid`, `transaction_age_seconds`, `query_age_seconds`, `state_age_seconds`, `lock_wait_seconds` (each `null` when unknown) |
| `long_transactions[]` | `pid`, `state`, `transaction_age_seconds`, `idle_in_transaction`, `direct_blocked_count`, oldest first |
| `warnings[]` | Analysis warnings (see codes above); collection warnings stay in `snapshot.warnings` |

The graph fields are defined in [blocking-graph.md](blocking-graph.md).
Example for the chain above:

```json
{
  "id": 116,
  "blocked_by": [],
  "blocks": [117],
  "direct_blocked_count": 1,
  "transitive_blocked_count": 2,
  "root": true,
  "in_cycle": false,
  "component": 1,
  "kind": "session"
}
```

## Versioning and compatibility

- `schema_version` is an integer. This build writes and reads version `1`.
- **Additive changes** (new optional fields) keep the version. Readers ignore
  unknown fields, so a file written by a newer build with extra fields still
  loads.
- **Breaking changes** (removing or renaming a field, changing a type or a
  meaning) increase the version.
- A reader rejects any other version with an explicit error
  (`unsupported snapshot schema_version N`) instead of guessing, and rejects
  files without `schema_version` as not being LockLens snapshots.
- Derived values are never stored, so improvements to the analysis apply to
  old files automatically.

## Privacy

Snapshot files never contain the DSN, password, or other credentials. Query
text is present only when collected with `--include-query`. Files are created
with mode `0600`; treat them as sensitive operational data anyway, because they
contain user names, application names, client addresses, and database names.
