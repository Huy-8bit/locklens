# Incidents

An incident is a bounded, append-only recording of what LockLens observed
around a problem: the samples before it was noticed, the trigger, the
evolution while it lasted, and how it ended. It is meant to answer "what
happened at 03:12?" after the fact, from evidence rather than memory.

## When an incident opens

After each activity collection the recorder evaluates the rules
([rules.md](rules.md)). An incident opens when one of the five **trigger
rules** has an active finding:

| Rule | Typical situation |
| --- | --- |
| `blocking.long_chain` | Sessions blocked behind the same root for 30 s and 3 samples |
| `blocking.idle_in_tx_blocker` | An idle-in-transaction session blocks others |
| `connections.pressure` | Connection usage at 80% of available slots or more |
| `queries.mean_regression` | A query became at least 2× slower per call between two windows |
| `replication.slot_retained_wal` | A replication slot retains more WAL than the threshold |

There is at most one open incident per target and trigger rule. Other active
findings during the incident are added to its timeline ("Also observed").

## What is recorded

The timeline is a list of events, written once and never edited:

| Event | Content |
| --- | --- |
| `pre_trigger` | Up to 30 samples from before the trigger, taken from an in-memory buffer: session/blocking summary, key metrics, active findings |
| `blocking_snapshot` | The full activity snapshot whenever the blocking graph changes shape (sessions, edges, waiting locks). Before the trigger, each distinct graph in the buffer is kept |
| `trigger` | The finding(s) that opened the incident, with evidence, explanation, next checks, and limitations |
| `visibility` | A warning that the monitoring role could not see all sessions |
| `workload` | Top queries by execution time around a workload trigger |
| `sample` | A summary at most every 60 s while the condition lasts, and on every graph change |
| `finding` | Another rule that became active during the incident |
| `gap` | No activity data for a period (collection failed or LockLens was down) |
| `retriggered` | The condition came back within the cooldown (see below) |
| `post_trigger` | Samples after the condition cleared, before resolution |
| `resolved` | Why recording ended |
| `recorder_restarted` | LockLens restarted while the incident was open |
| `truncated` | The size limit was reached |

Pre-trigger data exists only if LockLens was collecting before the problem
started; the UI says so.

## Lifecycle and limits

| Parameter | Value |
| --- | --- |
| Pre-trigger buffer | 30 samples per target (in memory) |
| Resolution | After 3 consecutive samples without the trigger condition (`condition cleared`) |
| Cooldown | 10 minutes: if the same rule fires again on the same target within 10 minutes of resolution, the previous incident is reopened with a `retriggered` event instead of creating a new one, so flapping conditions stay in one incident |
| Maximum duration | 2 hours: recording stops with `duration limit reached`. A new incident opens if the condition still persists after the cooldown |
| Maximum size | 1,000 events or 5 MiB per incident. Beyond that, samples and snapshots are no longer stored; lifecycle events (`trigger`, `retriggered`, `gap`, `recorder_restarted`, `resolved`) still are, and the incident is marked truncated |

**Restart recovery.** Incident metadata and stored events live in the
monitoring store. After a restart, open incidents are reattached, a
`recorder_restarted` event records the interruption, and recording continues.
The in-memory pre-trigger buffer and rule history are lost on restart, so
persistence-based rules need a few samples again before they can fire.

**Acknowledgement** (with an optional note) is metadata on the incident. It
never changes or hides evidence and does not stop recording.

Incidents are kept for `LOCKLENS_INCIDENT_RETENTION` (90 days) after
resolution, independently of raw history retention: they carry their own copy
of the evidence. Open incidents are never deleted by retention.

## Interpreting evidence

Findings report **observations** and **possible explanations**. LockLens does
not claim a root cause:

- Blocking is sampled at the collection interval; short waits between samples
  are invisible, and a snapshot is assembled from several catalog reads, so it
  is not atomic. A cycle in sampled data is not a confirmed deadlock.
- A query regression compares window means from `pg_stat_statements`; it
  does not identify a plan change (LockLens never runs `EXPLAIN`).
- With restricted visibility (no `pg_read_all_stats`), other roles' session
  details are unknown; the incident says so.

## Exports

From the incident page (**Export JSON**, **Export HTML report**), the API
(`GET /api/v1/incidents/{id}/export?format=json|html`), or the CLI:

```sh
LOCKLENS_STORE_DSN=... locklens incident export --id 42 --format json --out incident-42.json
LOCKLENS_STORE_DSN=... locklens incident export --id 42 --format html --out incident-42.html
locklens incident report incident-42.json --out incident-42.html   # offline, no database
```

**JSON** (`"format": "locklens-incident"`, `"schema_version": 1`): tool name
and version, export time, the incident, linked findings, and all events.
`query_text_included` says whether query text is present.

**HTML**: a single self-contained file with inline CSS — no JavaScript, no
external fonts, images, or requests, and its own restrictive Content Security
Policy. It contains the summary, trigger, findings with evidence, blocking
trees, and the timeline. It opens from disk or an email attachment without
LockLens.

**Privacy.** Exports never contain DSNs or passwords. Query text (session
queries in snapshots, statement text) is removed unless explicitly requested
(`include_query=true`, `--include-query`, or the checkbox), and it only
exists for targets that opted in to query text collection. All text is HTML
escaped; control characters are neutralised.

**Import.** **Incidents → Import an exported incident** (or `POST
/api/v1/incidents/import`) accepts an exported JSON file up to 10 MiB. The file is validated
(format, schema version, UTF-8, field lengths, event count) and stored as a
read-only incident with `source: import`. Files from a newer schema version are
rejected with a message instead of being guessed at.
