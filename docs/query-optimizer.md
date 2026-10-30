# Query optimizer

The optimizer turns each database's schema and workload into
**recommendations**: indexes to add or drop, statistics to refresh, memory
for statements that spill to disk, and statement shapes that often perform
badly. Each recommendation carries its evidence, a confidence, an estimated
benefit, and the SQL to apply it.

**LockLens never changes a database.** It shows the SQL; a person reviews it
and runs it. LockLens then notices the change in the catalog and measures the
statement before and after.

![Optimize](images/optimize.png)

![A recommendation](images/optimize-detail.png)

- [Where the data comes from](#where-the-data-comes-from)
- [Recommendations](#recommendations)
- [Benefit and confidence](#benefit-and-confidence)
- [What-if checks with HypoPG (optional)](#what-if-checks-with-hypopg-optional)
- [Life cycle: decide, apply, measure](#life-cycle-decide-apply-measure)
- [The SQL parser](#the-sql-parser)
- [Where to see it](#where-to-see-it)
- [Limitations](#limitations)

## Where the data comes from

| Source | Collector | Interval | Used for |
| --- | --- | --- | --- |
| `pg_index`, `pg_class`, `pg_constraint`, `pg_stat_user_tables`, `pg_stat_user_indexes`, `pg_extension`, planner settings | `catalog` | 15 min, and on request | Indexes with their columns and validity, table sizes and scan counters, foreign keys, `work_mem` |
| `pg_stats` — only `null_frac`, `n_distinct`, `correlation` | `catalog` | 15 min | Column selectivity for index column order. `most_common_vals` and histograms (data values) are never read; the view shows only columns the monitoring role may `SELECT` |
| Statement rollups and normalised texts | `statements` | 15 s | The busiest statements of the last day, their conditions, rows, I/O, temporary files |
| Saved plans | on request | — | Estimated rows for misestimates |

The catalog covers each database of the target's scope (the connection
string's by default; see [configuration.md](configuration.md#targets)), and
recommendations are kept per database: a statement is matched only with the
catalog of the database it ran in. What-if checks run only on the connection
string's database, where the workspace connects. The catalog reads system
catalogs and statistics views only, with relation sizes from `relpages`
(no size functions), so it takes no relation locks. The catalog is bounded
to the 2,000 largest tables and 5,000 indexes.

## Recommendations

| Kind | Found when | SQL |
| --- | --- | --- |
| **Missing index** | Busy statements filter a table of ≥ 10,000 rows on columns no valid B-tree index starts with. Columns: equalities (most selective first when statistics are visible), then one range column, or the `ORDER BY` columns of a statement with `LIMIT`; at most 4. Statements that need the same index are merged | `CREATE INDEX CONCURRENTLY …` |
| **Foreign key without index** | A referencing table of ≥ 1,000 rows has no valid index whose leading columns are the foreign key's. A warning when the referenced table is written to or deletes cascade: every DELETE there scans the referencing table while holding locks | `CREATE INDEX CONCURRENTLY …` |
| **Unused index** | `idx_scan = 0`, not unique, primary, or backing a constraint, ≥ 1 MiB. Low confidence when statistics were reset less than 7 days ago or the reset time is unknown. Scans on standbys are not counted here: check replicas first | `DROP INDEX CONCURRENTLY …` |
| **Redundant index** | Two valid B-tree indexes on the same columns, or one whose columns are a leading prefix of another's (the shorter is redundant). Indexes backing constraints are kept | `DROP INDEX CONCURRENTLY …` |
| **Invalid index** | `indisvalid` is false, usually after a failed `CREATE INDEX CONCURRENTLY` | `REINDEX INDEX CONCURRENTLY …` |
| **Stale statistics** | ≥ 20% of a table of ≥ 10,000 rows changed since it was last analysed, and not in the last day | `ANALYZE …` |
| **Row misestimate** | A saved plan estimates ≥ 10× more or fewer rows than the statement returns per call | `ANALYZE` of the tables in the plan; `CREATE STATISTICS` for several equality conditions on one table (correlated columns) |
| **Spills to disk** | A statement writes ≥ 8 MiB of temporary files per call (≥ 10 calls) | `SET LOCAL work_mem` for the transaction, or `ALTER ROLE … SET work_mem`, at about twice the spill rounded to a power of two (at most 1 GB) |
| **Statement shape** | A lint from PostgreSQL's parser: `SELECT *`, `OFFSET` pagination, a pattern starting with `%`, a function on a filtered column, `NOT IN (subquery)`, `OR` across columns, `UPDATE`/`DELETE` without `WHERE`, a cartesian join | — (a change in the application) |

Identifiers in generated SQL are quoted when PostgreSQL needs it. `CONCURRENTLY`
avoids blocking writes but cannot run inside a transaction block.

On a **standby** the schema is its primary's: structural recommendations
(foreign keys, unused, redundant, and invalid indexes, stale statistics) are
made for the primary only, where they can be applied. A standby's own workload
still yields statement recommendations, marked to be applied on the primary.
Unused-index recommendations on the primary do not see scans on standbys.

## Benefit and confidence

**Benefit** (0–100) orders recommendations: for statement-based kinds it
grows with the statements' share of all statement execution time; for
structural kinds (foreign keys, invalid and redundant indexes) it grows with
table and index size, with a floor so they are never lost. It is not a
prediction of the speed-up.

**Confidence** grades the evidence:

- *high*: verified with a hypothetical index (the planner would use it, at half
  the estimated cost or less), or a structural fact (an invalid index, a
  foreign key without an index, identical indexes);
- *medium*: supported by counters (a table read mostly sequentially, many
  modified rows) or a plan;
- *low*: derived from the statement text only, or from statistics that are
  too young.

## What-if checks with HypoPG (optional)

[HypoPG](https://github.com/HypoPG/hypopg) creates *hypothetical* indexes:
they exist only in the memory of one session, are never written to the
catalog, and are visible only to the planner. With it, LockLens can ask
"would PostgreSQL use this index, and how much cheaper would the plan be?"
without creating anything.

It is off by default. To use it:

1. The database owner installs the extension on the target
   (`CREATE EXTENSION hypopg`, after installing the package for the server's
   version). LockLens never installs extensions.
2. In **Explorer → access settings** of the instance, enable the read-only SQL
   editor and **What-if checks with HypoPG**.
3. On a missing-index or foreign-key recommendation, choose **Check with a
   hypothetical index** (operators and administrators with the SQL permission).

The check runs through the investigation workspace with its credentials,
policy, read-only transaction, time limits, and audit log (action `whatif`).
For each statement it plans the statement (`EXPLAIN (FORMAT JSON)`, with
`GENERIC_PLAN` for statements with `$n` parameters, PostgreSQL 16+), creates
the index with `hypopg_create_index`, plans again, and calls `hypopg_reset()`.
Nothing is executed. The result (estimated cost before and after, whether the
plan uses the index) is stored with the recommendation and sets its
confidence.

## Life cycle: decide, apply, measure

| Status | Meaning |
| --- | --- |
| open | Derived from the current catalog and workload |
| accepted / dismissed | A person decided, with a note (metadata only) |
| applied | The change appeared in the catalog: the index exists (or was dropped, became valid, the table was analysed). Time per call before the change is recorded (median of five-minute means over the 7 days before) |
| improved (`verified`) | After at least 30 minutes of the statement running after the change, its time per call is at least 20% lower |
| no measurable effect (`no_effect`) | Within ±20% |
| slower after change (`regressed`) | At least 20% higher. Measurement continues for a day, so an improvement can still turn into a regression; the UI flags it and a `recommendation.updated` event is published |
| no longer applies (`resolved`) | An open recommendation was not derived again (the statement stopped, the table shrank, someone fixed it differently). It reopens if it comes back |

Applied changes are found in the catalog collected after them (at most 15
minutes, or at once with **Analyse now**, which collects a fresh catalog
first). Resolved and dismissed recommendations are removed after
`LOCKLENS_FINDING_RETENTION`.

## The SQL parser

Statement analysis uses PostgreSQL's own parser (libpg_query through
[pg_query_go](https://github.com/pganalyze/pg_query_go)) on the normalised
`pg_stat_statements` text, so conditions, joins, subqueries, and CTEs are read
as PostgreSQL reads them. The parser is C code: the server is built with cgo
(the Docker image links it statically). A build with `CGO_ENABLED=0` still
works; it reports the parser as unavailable and makes only catalog-based
recommendations.

Statement-based recommendations need query text collection for the target
(on by default).

## Where to see it

- **Optimize** (Investigate → Optimize): every instance's recommendations
  ranked by benefit; per instance, when the catalog was collected and whether
  HypoPG is available; filters by status and kind; a drawer with the
  evidence, the statements, the SQL, the what-if check, the decision, and the
  history.
- **Statement page → Optimization**: the parser's view of the statement
  (conditions an index could serve, lints) and the recommendations that name
  it.

API: [api.md](api.md#query-optimizer).

## Limitations

- Index proposals come from statement conditions and counters, not from
  execution: confirm with HypoPG or `EXPLAIN` on a copy, and remember that
  every index costs writes and space.
- `idx_scan` counts only the server LockLens observes; an index used only on
  a standby looks unused on the primary.
- Column statistics are often not visible to the monitoring role; then column
  order follows the statements.
- Generic plans (statements with parameters) need PostgreSQL 16 or later for
  what-if checks.
- Before/after measurement compares means per period; a change in traffic or
  data at the same time affects it too.
