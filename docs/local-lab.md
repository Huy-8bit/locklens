# Local workload sandbox

`local-lab/` is an **unversioned, machine-local sandbox**. The root `.gitignore`
excludes the entire directory before any files are created. It is also excluded
from the application Docker build context. It is not included in a clone or a
release. Application changes and regression tests remain version controlled;
`demo/`, `deploy/`, and `tests/` are not ignored.

The sandbox on this workstation contains its own Compose project
(`locklens-workload`), PostgreSQL, a Go CRUD HTTP API, initialization/seed SQL,
a bounded HTTP generator, lifecycle scripts, and local output. It never uses the
LockLens monitoring store as the workload database. A fresh clone can use
`make up` and `make lab` for the versioned demonstration; these sandbox commands
require the separately provisioned `local-lab/` directory.

## Versioned PostgreSQL demo topology

The standard `make up` Compose stack runs one writable PostgreSQL primary, two
physical streaming standbys, and a small Go CRUD API with a built-in synthetic
client workload. Both standbys use separate replication slots and data volumes.
The LockLens bootstrap targets include the primary and both standbys.

| Service | Local endpoint |
| --- | --- |
| Primary | `127.0.0.1:55432` |
| Standby 1 | `127.0.0.1:55433` |
| Standby 2 | `127.0.0.1:55436` |
| Backend API / health | `http://127.0.0.1:8091/items` / `http://127.0.0.1:8091/healthz` |

The backend service continuously exercises `GET`, `POST`, `PUT`, and `DELETE`
against the primary, with a default two workers and a two-second interval per
worker. Override `LOCKLENS_WORKLOAD_INTERVAL` or
`LOCKLENS_WORKLOAD_CONCURRENCY` in the environment to change the rate. The
workload uses a dedicated `backend_workload_items` table and the `demo_app` role;
it does not write to LockLens' monitoring store or the existing shop tables.
The API exposes `GET /items`, `POST /items` with `{"payload":"..."}`,
`GET /items/{id}`, `PUT /items/{id}` with the same JSON body, and
`DELETE /items/{id}`. Requests and the automatic workload are synthetic.

Start or update the stack with `make up`; inspect replication with
`docker compose -f deploy/compose.yaml exec primary psql -U postgres -d locklens_demo -c
"SELECT application_name, state, sync_state FROM pg_stat_replication"` and inspect
the API at `http://127.0.0.1:8091/items`.

## Start and connect

Run from the repository root:

```sh
make up
./local-lab/scripts/lab.sh start
./local-lab/scripts/lab.sh status
python3 local-lab/scripts/register-target.py
```

The registration helper prompts for the existing LockLens administrator password
and uses the normal authenticated target API. It does not modify the monitoring
store directly. It reuses a target named `workload-sandbox` if one already exists.
Credentials are stored only in the ignored sandbox's `.env` / `.env.example`.

| Service | Local endpoint |
| --- | --- |
| LockLens UI | http://127.0.0.1:8080 |
| Fake CRUD API / readiness | http://127.0.0.1:8091 /health |
| Workload PostgreSQL | 127.0.0.1:55435 |
| Monitoring store (separate) | 127.0.0.1:55434 |

All published ports bind to loopback. The Docker-hosted LockLens target connects
through `host.docker.internal:55435` on Docker Desktop. For a host-native server,
set `LAB_TARGET_HOST=127.0.0.1` when registering. On Linux, configure Docker's
host-gateway mapping or an explicit private network before registering; the
sandbox does not silently expose PostgreSQL on all interfaces.

The database is `locklens_workload_lab`, with distinct application and monitoring
roles, `pg_stat_statements`, 10,000 seeded items by default, and identifiable
`application_name` values. Initialization only runs on a new volume. The
application refuses a differently named database. The API supports paginated
`GET /items`, `POST /items`, and `GET`, `PUT`, `DELETE /items/{id}`. Listing defaults
to 25 items, with a maximum of 100. The service validates JSON, bounds request and
pool timeouts, and handles termination gracefully.

## Workloads

```sh
./local-lab/scripts/lab.sh load --scenario read-heavy --rps 25 --concurrency 8 --duration 2m
./local-lab/scripts/lab.sh load --scenario write-heavy --rps 50 --concurrency 8 --duration 2m
./local-lab/scripts/lab.sh load --scenario mixed --read 50 --write 40 --delete 10 --duration 2m
./local-lab/scripts/lab.sh load --scenario burst --rps 100 --concurrency 16 --duration 2m
```

Burst traffic schedules requests for five seconds out of every twenty seconds.
The bounded queue drops schedules when workers are busy instead of creating
unlimited goroutines. Delete operations create then delete a disposable item so
the seeded ID range remains usable; these operations produce two HTTP requests.
Consequently achieved HTTP RPS can exceed the scheduled operation rate.

`--dataset-size` selects the seeded ID range; set `LAB_DATASET_SIZE` before the
first start (or an explicit reset) to change the seed size. Do not select a larger
ID range than was seeded. `--hot-key-ratio` selects how often the hot-row scenario
uses item 1. Limits: 1–500 scheduled operations/s, 1–44 workers, 1 second–30 minutes,
100–100,000 seeded IDs, and ratios summing to 100.

Fault scenarios require both service and generator opt-in:

```sh
./local-lab/scripts/lab.sh faults-on
./local-lab/scripts/lab.sh load --scenario hot-row --allow-faults --hot-key-ratio 0.9 --rps 30 --concurrency 12 --duration 1m
./local-lab/scripts/lab.sh load --scenario slow --allow-faults --rps 5 --concurrency 8 --duration 1m
./local-lab/scripts/lab.sh load --scenario blocking --allow-faults --rps 4 --concurrency 6 --duration 110s
./local-lab/scripts/lab.sh load --scenario connections --allow-faults --rps 40 --concurrency 44 --duration 1m
./local-lab/scripts/lab.sh faults-off
```

The fault routes exist only in the ignored fake service, require a local token,
and are absent from the LockLens server. Transactions roll back on completion,
client cancellation, or interruption. Blocking holds an idle transaction for at
most 120 seconds; waiting CRUD requests time out. Connection pressure uses at
most 44 fault requests and a 48-connection application pool against a database
limited to 60 connections. These bounds leave monitoring capacity.

The generator prints actual completed HTTP request rate, errors, dropped
schedules, and measured end-to-end HTTP latency percentiles. **API latency is not
PostgreSQL query latency.** A request cancelled exactly at the run deadline is
reported separately; intentional blocking can produce timeout errors.

## Observe and clean up

Select `workload-sandbox`, use a 5-minute window and 5-second refresh, and watch
transactions/s, WAL bytes/s, sessions, and connections while switching workloads.
The query workload page uses the statement collector's slower interval (normally
60 seconds); allow at least two collections. Enable query text only for this
synthetic target if needed. Low activity between bursts is a real result.

During blocking, open **Blocking explorer**, select a session, and use graph
zoom/pan/fit. On Overview, click an incident annotation or drag a time selection.
Every chart shares that selection and the API is queried for the selected bounds.
**Pause** freezes the timeline and automatic refresh. **View incident time range**
opens a fixed range around recorded evidence. Return to live mode to see recovery.

A run stops at its duration or on Ctrl-C. Then:

```sh
./local-lab/scripts/lab.sh faults-off
./local-lab/scripts/lab.sh status
./local-lab/scripts/lab.sh stop
# Destructive only to this sandbox's disposable database:
./local-lab/scripts/lab.sh reset --yes

git check-ignore local-lab/.env local-lab/output/example.jsonl
git ls-files local-lab
```

The last command must print nothing. Never force-add the sandbox. Reset does not
remove LockLens history, its store, or the versioned demonstration database.

## Relational investigation lab

The ignored sandbox now includes synthetic `customers`, `products`, `orders` and
`order_items`, with primary/foreign keys, indexes, JSONB attributes and an isolated
`lab_reader` role. Existing volumes are upgraded without resetting them:

```sh
docker compose -f local-lab/compose.yaml exec -T postgres \
  psql -v ON_ERROR_STOP=1 -U lab_admin -d locklens_workload_lab \
  < local-lab/postgres/relations.sql
./local-lab/scripts/lab.sh start
# Prompts/credentials belong only to this local sandbox; never commit its .env.
LOCKLENS_PASSWORD='<your local UI password>' python3 local-lab/scripts/configure-workspace.py
```

Open `workload-sandbox` → Database explorer → `public` → `orders` or `items`.
Use Load first page; metadata does not automatically load application rows.
The fake service exposes item POST/GET/PUT/DELETE and relational POST/GET `/orders`.
A POST order body is `{"customer_id":1,"product_id":2,"quantity":3}`. All rows are
synthetic. The browser verification creates an item, verifies it, updates it,
verifies the changed name, deletes only that item and verifies its absence.

```sh
./local-lab/scripts/lab.sh load --scenario read-heavy --rps 30 --concurrency 8 --duration 45s
./local-lab/scripts/lab.sh load --scenario write-heavy --rps 35 --concurrency 8 --duration 60s
./local-lab/scripts/lab.sh faults-on
./local-lab/scripts/lab.sh load --scenario blocking --allow-faults --rps 4 --concurrency 8 --duration 60s
./local-lab/scripts/lab.sh faults-off
```

Observe Target overview → logical tuple writes / WAL; Query workload → observed
statement calls; Sessions and Connections → sampled applications; Blocking → blocker
and waiters; Incidents → recorded timeline. `long-transaction` and
`query-regression` are explicit fault scenarios (aliases of bounded held-transaction
and slow-query behaviors), and do not represent a production regression detector.
For replication, the existing `make up` Compose primary/physical standby is used,
with an explicit mapping recording `deploy/compose.yaml` as provenance.

The sandbox remains ignored, so a fresh clone does not include these private local
files. The tracked demo and isolated E2E harness remain reproducible without them.
`make up` provides the tracked primary/standby lab; workspace E2E provisions a dedicated
read-only role and synthetic table automatically. Do not copy real credentials into
these examples. No faults are left enabled after verification.
