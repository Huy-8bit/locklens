# Quickstart

Two ways to start: the **Docker lab** (about 10 minutes, nothing to install
but Docker) or **your own databases** with a locally built binary.

## Option A: the Docker lab

### Prerequisites

- Docker with Compose v2 (Docker Desktop, OrbStack, Colima, or Docker Engine).
- About 2 GB of free memory and 2 GB of disk for images and volumes.
- Free loopback ports 8080 (UI), 55432 (lab primary), 55433 (lab standby),
  55434 (monitoring store). To use others, set `LOCKLENS_HTTP_PORT`,
  `LOCKLENS_PG_PORT`, `LOCKLENS_STANDBY_PORT`, `LOCKLENS_STORE_PORT`.
- `make` (or run the `docker compose` commands shown in the
  [Makefile](../Makefile) directly). Go and Node.js are **not** needed: the
  image builds the UI and the server.

### 1. Start

```sh
git clone <this repository> locklens && cd locklens
make lab-up
```

`make lab-up` builds the `locklens-backend:local` and `locklens-frontend:local` images (the first build takes a few
minutes) and starts the lab, project `locklens-lab`, on loopback only, waiting
until each container is healthy:

| Container | Role |
| --- | --- |
| `store` | PostgreSQL 18.6: LockLens' monitoring store |
| `primary` | PostgreSQL 18.6 to monitor, with `pg_stat_statements`, demo roles, and replication slots |
| `standby`, `standby-2` | Hot standbys streaming from `primary` |
| `backend-simulator` | A small API with continuous synthetic traffic on `primary` |
| `node-exporter` | Host metrics of the Docker VM |
| `backend` | `locklens server` in demo mode, with the three databases pre-registered as targets |
| `frontend` | The web UI (nginx), proxying `/api/` to `backend`; the only container published to your machine (port 8080) |

`make up` (without `lab`) starts only LockLens and its store, for your own
databases; see [Option B](#option-b-your-own-databases). The two use separate
Compose projects and data, but the same port 8080, so stop one before starting
the other (or set `LOCKLENS_HTTP_PORT`).

### 2. Sign in

Open <http://127.0.0.1:8080> and sign in as **admin** / **locklens-lab-admin**.

These credentials, the database passwords, and the master key in
[deploy/lab.env](../deploy/lab.env) and
[deploy/compose.lab.yaml](../deploy/compose.lab.yaml) are fake, local-only lab values.
Never use them elsewhere.

You land on the **Overview** of the whole environment; **Databases** lists
`lab-primary` and `lab-standby` (environment `lab`). Within about 20 seconds
both should be `healthy`, with current values and charts filling as samples
arrive, and **Topology** shows the standby streaming from the primary. The lab
primary also contains a small demo shop (`shop` schema: customers, products,
orders, order_items, a view, a function, an enum type, and a trigger) that you
can browse under **Explorer** and query read-only in the **SQL Editor**; both
use a read-only role that the lab configures for you. The lab also
runs a node_exporter inside Docker, so **System resources** shows the Docker
VM's CPU, memory, and filesystem (not your computer's; `make lab-up-host` adds an
exporter that reads the host on Linux).

### 3. Run scenarios

Each scenario runs in a separate, short-lived `lab` container that connects
to the lab primary with the demo application role, holds the situation for a
bounded time, and cleans up after itself.

```sh
make lab-list                          # scenarios and default durations
make lab SCENARIO=chain                # run one (Ctrl+C ends it early and cleans up)
make lab SCENARIO=chain DURATION=90s   # custom duration
make lab-status                        # lab sessions and objects currently present
make lab-stop                          # end every lab session and undo lab changes
```

| Scenario | Duration | What happens | Where to look | Expected result (demo thresholds) |
| --- | --- | --- | --- | --- |
| `healthy` | 10 min | Light reads and small updates | Overview, Queries | Charts with activity, no findings |
| `shop` | 10 min | Mixed shop traffic: product and order reads, new orders, status updates, deletes of cancelled orders (~40 statements/s) | Queries, Overview | SELECT, INSERT, UPDATE, DELETE, and transaction control each visible per type over time |
| `row-blocking` | 3 min | A updates a row and keeps working; B waits (A → B) | Blocking, Sessions | `blocking.long_chain` incident after ~20 s |
| `chain` | 3 min | A → B → C on two rows | Blocking | Tree with root A blocking 2 in total; incident |
| `idle-blocker` | 3 min | A stays idle in transaction holding a row lock; B and C wait | Blocking, Sessions | Critical `blocking.idle_in_tx_blocker` incident; `transactions.long` after 60 s |
| `connection-pressure` | 3 min | Idle connections up to ~92% of available slots | Connections, Overview | `connections.pressure` incident |
| `regression` | 6 min | A query runs with an index, then the index is dropped (restored at the end) | Queries (compare windows) | `queries.mean_regression` incident after the second phase starts |
| `temp-files` | 5 min | Sorts with `work_mem=64kB` spill to temp files | Overview (temp files) | `temp_files.increase` finding |
| `replication` | 4 min | Standby replay paused, an inactive slot created, WAL written | Topology, Database → Replication | `replication.slot_retained_wal` incident, `replication.replica_lag` findings |
| `outage` | 60 s | `make lab-outage` stops the primary, then starts it again | Databases, Overview | `lab-primary` becomes `unreachable`, then recovers; the gap is shown in charts |

Timings are approximate and assume demo mode (`LOCKLENS_DEMO_MODE=true`),
which lowers thresholds so findings appear within minutes. Production
defaults are deliberately slower ([rules.md](rules.md)).

### 4. Investigate an incident

1. **Alerts** lists the instance's incidents and current findings; open an incident.
2. The timeline shows pre-trigger samples, the trigger finding with evidence,
   blocking snapshots ("Show blocking graph"), and how it resolved.
3. **Export HTML report** downloads a standalone file that opens offline;
   **Export JSON** is the machine-readable form (import it on another
   LockLens instance under **Settings → Data and storage** (Import an incident)).

### 5. Optional: host metrics

```sh
make lab-up-host
```

starts node_exporter as well. In **Settings → Instances → lab-primary → Edit**, set the
node_exporter URL to `http://node-exporter:9100/metrics`. On Docker Desktop
and similar, it reports the Linux VM's CPU and memory, not your computer's.

### 6. Use the CLI against the lab

```sh
make build    # needs Go 1.26+
export LOCKLENS_DSN='postgres://locklens_observer:observer-pw@127.0.0.1:55432/locklens_demo?sslmode=disable'
bin/locklens doctor
bin/locklens inspect
make demo     # the original CLI walkthrough: row lock, chain, idle transaction
```

### 7. Stop and clean up

```sh
make lab-down     # removes the lab's containers and volumes (project "locklens-lab" only)
```

## Option B: your own databases

### With Docker (recommended)

```sh
make up      # backend, frontend, and store only, on http://127.0.0.1:8080
make down    # stop; the data is kept
```

The first `make up` writes `deploy/.env` (mode 600, ignored by git) with a
random store password, master key, and administrator password, and prints
the password. Keep the master key: it encrypts stored connection strings.
Edit `deploy/.env` (see [deploy/.env.example](../deploy/.env.example)) to
choose your own values; changing `LOCKLENS_ADMIN_PASSWORD` and running
`make up` again resets the administrator's password. Then add your databases
under **Settings → Instances → Add instance**; from the container, use an address it can
reach (a Docker service name or `host.docker.internal`), not `localhost`.

### Build from source

Requires Go 1.26+ and Node.js 22+ (for the UI).

```sh
make build-all    # web UI (web/dist) + bin/locklens (API server and CLI)
```

### Prepare

1. A PostgreSQL 14+ database for the monitoring store, for example:

   ```sql
   CREATE ROLE locklens_store LOGIN PASSWORD '...';
   CREATE DATABASE locklens OWNER locklens_store;
   ```

2. On each database to monitor, a read-only monitoring role:

   ```sql
   CREATE ROLE locklens_observer LOGIN PASSWORD '...';
   GRANT pg_read_all_stats TO locklens_observer;
   ```

   Optional, for workload analysis: `pg_stat_statements` in
   `shared_preload_libraries` and `CREATE EXTENSION pg_stat_statements;` in the
   database LockLens connects to.

### Run

```sh
export LOCKLENS_STORE_DSN='postgres://locklens_store:...@store-host:5432/locklens?sslmode=verify-full'
export LOCKLENS_MASTER_KEY="$(openssl rand -base64 32)"   # keep it: it decrypts stored DSNs
read -rs LOCKLENS_ADMIN_PASSWORD && export LOCKLENS_ADMIN_PASSWORD
bin/locklens server   # the API on 127.0.0.1:8080
```

The server serves the API only. For the UI, run the frontend image against it
(`docker run -p 127.0.0.1:3000:8080 -e LOCKLENS_BACKEND_URL=http://host.docker.internal:8080 locklens-frontend:local`
after `make image`), or Vite during development
(`cd web && LOCKLENS_DEV_API=http://127.0.0.1:8080 npm run dev`). Open the UI,
sign in as `admin`, and add instances under **Settings → Instances → Add instance**. When
the server listens beyond loopback, see [deployment.md](deployment.md).

Next: [configuration.md](configuration.md) for all settings,
[security.md](security.md) before exposing the server beyond your machine.

## Add your own database

Under **Settings → Instances → Add instance**, paste a connection string and choose
**Check connection**:

- a regular role (for example one with `pg_monitor`) is used as is;
- a superuser: LockLens lists the `CREATE ROLE` and `GRANT` statements it would
  run, creates `locklens_monitor` and `locklens_reader` after you confirm, and
  stores only those roles; add the primary first, then its standbys, which
  reuse the roles.

From a LockLens container, use an address the container can reach (for
example the database's Docker service name, or `host.docker.internal` for a
database published on your machine), not `localhost`.

To give colleagues access, add users under **Settings → Users and roles**: viewers read
the dashboards, operators can also preview rows and run read-only SQL.

