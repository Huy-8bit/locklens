// Package lab implements the LockLens incident lab scenarios. It WRITES to
// the lab database (fake data only) and is never imported by the locklens
// binary. Every scenario runs for a bounded duration and cleans up what it
// created; Stop cleans up scenarios started by another process.
package lab

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/locklens/locklens/demo/scenario"
)

// AppPrefix marks every lab connection in pg_stat_activity.
const AppPrefix = "locklens-lab-"

// Lab objects created by scenarios.
const (
	staleSlot     = "lab_stale_slot"
	ordersIndex   = "lab_orders_customer_idx"
	walChurnTable = "lab_wal_churn"
)

// Config holds the lab connection strings.
type Config struct {
	DSN             string // demo_app: data owner, runs the workload
	AdminDSN        string // superuser on the primary (slots, terminating lab sessions)
	StandbyAdminDSN string // superuser on the standby (replay pause/resume)
	Out             io.Writer
}

// Scenario describes one lab scenario.
type Scenario struct {
	Name, Description string
	Default           time.Duration
	Run               func(ctx context.Context, c Config, d time.Duration) error
}

// Scenarios lists the runner-driven scenarios. The outage scenario is driven
// from the host (scripts/lab-outage.sh) because it stops a container.
var Scenarios = []Scenario{
	{"healthy", "light background workload of reads and small updates", 10 * time.Minute, runHealthy},
	{"row-blocking", "A updates a row and keeps working; B waits for it (A -> B)", 3 * time.Minute, runRowBlocking},
	{"chain", "A holds row 1; B holds row 2 and waits for row 1; C waits for row 2 (A -> B -> C)", 3 * time.Minute, runChain},
	{"idle-blocker", "A updates a row and stays idle in transaction while B and C wait", 3 * time.Minute, runIdleBlocker},
	{"connection-pressure", "opens idle connections up to about 92% of available slots", 3 * time.Minute, runConnectionPressure},
	{"regression", "a query runs with an index, then the index is dropped so the same query gets slower; the index is restored at the end", 6 * time.Minute, runRegression},
	{"temp-files", "sorts with a tiny work_mem so data spills to temporary files", 5 * time.Minute, runTempFiles},
	{"shop", "mixed shop traffic on the shop schema: product and order reads, new orders, status updates, deletes of cancelled orders", 10 * time.Minute, runShop},
	{"replication", "pauses replay on the standby and creates an inactive slot while writing WAL; restored at the end", 4 * time.Minute, runReplication},
}

// Find returns a scenario by name.
func Find(name string) (Scenario, bool) {
	for _, s := range Scenarios {
		if s.Name == name {
			return s, true
		}
	}
	return Scenario{}, false
}

func logf(c Config, format string, args ...any) {
	fmt.Fprintf(c.Out, "%s  "+format+"\n", append([]any{time.Now().Format("15:04:05")}, args...)...)
}

func pool(ctx context.Context, dsn, app string, max int32) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, errors.New("invalid lab DSN")
	}
	cfg.MaxConns = max
	cfg.ConnConfig.RuntimeParams["application_name"] = AppPrefix + app
	// Safety nets in case the runner is killed.
	cfg.ConnConfig.RuntimeParams["idle_in_transaction_session_timeout"] = "900000"
	p, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, err
	}
	return p, nil
}

// Setup creates the lab tables with synthetic data if they do not exist.
func Setup(ctx context.Context, c Config) error {
	p, err := pool(ctx, c.DSN, "setup", 1)
	if err != nil {
		return err
	}
	defer p.Close()
	_, err = p.Exec(ctx, `
CREATE TABLE IF NOT EXISTS lab_accounts (id int PRIMARY KEY, owner text NOT NULL, balance numeric(12,2) NOT NULL);
INSERT INTO lab_accounts SELECT g, 'customer-' || g, 100 FROM generate_series(1, 10000) g ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS lab_orders (id bigserial PRIMARY KEY, customer_id int NOT NULL, amount numeric(12,2) NOT NULL, note text NOT NULL);
INSERT INTO lab_orders (customer_id, amount, note)
SELECT (random() * 4999)::int + 1, round((random() * 500)::numeric, 2), md5(g::text)
FROM generate_series(1, 200000) g WHERE NOT EXISTS (SELECT 1 FROM lab_orders LIMIT 1);
CREATE INDEX IF NOT EXISTS `+ordersIndex+` ON lab_orders (customer_id);
ANALYZE lab_accounts; ANALYZE lab_orders;`)
	return err
}

// pace runs fn about rate times per second until ctx ends. It is load
// pacing, not synchronisation.
func pace(ctx context.Context, rate float64, fn func(context.Context) error) error {
	t := time.NewTicker(time.Duration(float64(time.Second) / rate))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
			if err := fn(ctx); err != nil && ctx.Err() == nil {
				return err
			}
		}
	}
}

func runHealthy(ctx context.Context, c Config, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	p, err := pool(ctx, c.DSN, "healthy", 4)
	if err != nil {
		return err
	}
	defer p.Close()
	logf(c, "healthy workload running for %s", d)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pace(ctx, 5, func(ctx context.Context) error {
				id := rand.IntN(10000) + 1
				if rand.IntN(4) == 0 {
					_, err := p.Exec(ctx, "UPDATE lab_accounts SET balance = balance + 1 WHERE id = $1", id)
					return err
				}
				var b float64
				return p.QueryRow(ctx, "SELECT balance::float8 FROM lab_accounts WHERE id = $1", id).Scan(&b)
			})
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// runShop drives a mixed workload on the demo shop schema
// (demo/postgres/03-demo-data.sql) so the workload views show every kind of
// statement: about 60% reads, 20% inserts, 15% updates, 5% deletes.
func runShop(ctx context.Context, c Config, d time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	p, err := pool(ctx, c.DSN, "shop", 4)
	if err != nil {
		return err
	}
	defer p.Close()
	var exists bool
	if err := p.QueryRow(ctx, "SELECT to_regclass('shop.orders') IS NOT NULL").Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return errors.New("the shop schema does not exist: it is created when the lab primary is first initialised; recreate the lab with `make down && make up`")
	}
	logf(c, "shop traffic running for %s (about 40 statements per second)", d)
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- pace(ctx, 10, func(ctx context.Context) error { return shopStep(ctx, p) })
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil && !errors.Is(e, context.DeadlineExceeded) && !errors.Is(e, context.Canceled) {
			return e
		}
	}
	return nil
}

func shopStep(ctx context.Context, p *pgxpool.Pool) error {
	switch r := rand.IntN(100); {
	case r < 25:
		var name string
		var price int
		return p.QueryRow(ctx, "SELECT name, price_cents FROM shop.products WHERE id = $1", rand.IntN(200)+1).Scan(&name, &price)
	case r < 45:
		rows, err := p.Query(ctx, "SELECT id, status, total_cents FROM shop.orders WHERE customer_id = $1 ORDER BY created_at DESC LIMIT 20", rand.IntN(500)+1)
		if err != nil {
			return err
		}
		rows.Close()
		return rows.Err()
	case r < 55:
		var n int64
		return p.QueryRow(ctx, "SELECT count(*) FROM shop.orders WHERE status = $1 AND created_at > now() - interval '1 day'",
			[]string{"pending", "paid", "shipped"}[rand.IntN(3)]).Scan(&n)
	case r < 60:
		rows, err := p.Query(ctx, `SELECT p.category, sum(i.quantity * i.price_cents) AS revenue
FROM shop.order_items i JOIN shop.products p ON p.id = i.product_id
JOIN shop.orders o ON o.id = i.order_id WHERE o.created_at > now() - interval '6 hours'
GROUP BY p.category ORDER BY revenue DESC`)
		if err != nil {
			return err
		}
		rows.Close()
		return rows.Err()
	case r < 80:
		return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
			var id int64
			if err := tx.QueryRow(ctx, "INSERT INTO shop.orders (customer_id, status, total_cents) VALUES ($1, 'pending', 0) RETURNING id",
				rand.IntN(500)+1).Scan(&id); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "INSERT INTO shop.order_items (order_id, product_id, quantity, price_cents) SELECT $1, id, $2, price_cents FROM shop.products WHERE id = $3",
				id, rand.IntN(3)+1, rand.IntN(200)+1); err != nil {
				return err
			}
			_, err := tx.Exec(ctx, "UPDATE shop.orders SET total_cents = (SELECT sum(quantity * price_cents) FROM shop.order_items WHERE order_id = $1) WHERE id = $1", id)
			return err
		})
	case r < 95:
		_, err := p.Exec(ctx, "UPDATE shop.orders SET status = $1 WHERE id = (SELECT id FROM shop.orders WHERE status = $2 ORDER BY id DESC LIMIT 1)",
			[]string{"paid", "shipped", "cancelled"}[rand.IntN(3)], []string{"pending", "paid"}[rand.IntN(2)])
		return err
	default:
		return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
			var id int64
			err := tx.QueryRow(ctx, "SELECT id FROM shop.orders WHERE status = 'cancelled' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED").Scan(&id)
			if errors.Is(err, pgx.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, "DELETE FROM shop.order_items WHERE order_id = $1", id); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, "DELETE FROM shop.orders WHERE id = $1", id)
			return err
		})
	}
}

func hold(ctx context.Context, c Config, d time.Duration, what string) {
	logf(c, "%s established; holding for %s (Ctrl+C or `locklens-lab stop` ends it early)", what, d)
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

func newScenario(ctx context.Context, c Config, name string) (*scenario.Scenario, error) {
	scenario.AppPrefix = AppPrefix
	return scenario.New(ctx, name, c.DSN, "lab_contention")
}

// runRowBlocking keeps A active (sleeping inside its transaction) so the
// root blocker is not idle in transaction.
func runRowBlocking(ctx context.Context, c Config, d time.Duration) error {
	s, err := newScenario(ctx, c, "row-blocking")
	if err != nil {
		return err
	}
	defer closeQuietly(c, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		return err
	}
	if err := s.Exec(ctx, a, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1"); err != nil {
		return err
	}
	s.ExecAsync(a, "SELECT pg_sleep($1)", d.Seconds()+30)
	b, err := s.Begin(ctx, "B")
	if err != nil {
		return err
	}
	s.ExecAsync(b, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1")
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		return err
	}
	logf(c, "A=pid %d (active, sleeping in its transaction) blocks B=pid %d", a.PID, b.PID)
	hold(ctx, c, d, "row blocking")
	return nil
}

func runChain(ctx context.Context, c Config, d time.Duration) error {
	s, err := newScenario(ctx, c, "chain")
	if err != nil {
		return err
	}
	defer closeQuietly(c, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		return err
	}
	if err := s.Exec(ctx, a, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1"); err != nil {
		return err
	}
	s.ExecAsync(a, "SELECT pg_sleep($1)", d.Seconds()+30)
	b, err := s.Begin(ctx, "B")
	if err != nil {
		return err
	}
	if err := s.Exec(ctx, b, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 2"); err != nil {
		return err
	}
	s.ExecAsync(b, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1")
	if err := s.WaitBlocked(ctx, b.PID, a.PID); err != nil {
		return err
	}
	cc, err := s.Begin(ctx, "C")
	if err != nil {
		return err
	}
	s.ExecAsync(cc, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 2")
	if err := s.WaitBlocked(ctx, cc.PID, b.PID); err != nil {
		return err
	}
	logf(c, "chain A=pid %d -> B=pid %d -> C=pid %d", a.PID, b.PID, cc.PID)
	hold(ctx, c, d, "blocking chain")
	return nil
}

func runIdleBlocker(ctx context.Context, c Config, d time.Duration) error {
	s, err := newScenario(ctx, c, "idle-blocker")
	if err != nil {
		return err
	}
	defer closeQuietly(c, s)
	a, err := s.Begin(ctx, "A")
	if err != nil {
		return err
	}
	if err := s.Exec(ctx, a, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1"); err != nil {
		return err
	}
	var waiters []int
	for _, label := range []string{"B", "C"} {
		w, err := s.Begin(ctx, label)
		if err != nil {
			return err
		}
		s.ExecAsync(w, "UPDATE "+s.Ident()+" SET balance = balance + 1 WHERE id = 1")
		waiters = append(waiters, w.PID)
	}
	if err := s.WaitBlocked(ctx, waiters[0], a.PID); err != nil {
		return err
	}
	logf(c, "A=pid %d is idle in transaction and blocks pids %v", a.PID, waiters)
	hold(ctx, c, d, "idle-in-transaction blocker")
	return nil
}

func closeQuietly(c Config, s *scenario.Scenario) {
	if err := s.Close(context.Background()); err != nil {
		logf(c, "cleanup note: %v", err)
	}
	logf(c, "scenario sessions rolled back and closed")
}

func runConnectionPressure(ctx context.Context, c Config, d time.Duration) error {
	admin, err := pool(ctx, c.AdminDSN, "admin", 1)
	if err != nil {
		return err
	}
	defer admin.Close()
	var maxConn, reserved, used int
	if err := admin.QueryRow(ctx, `
SELECT current_setting('max_connections')::int,
       current_setting('superuser_reserved_connections')::int + coalesce(nullif(current_setting('reserved_connections', true), '')::int, 0),
       (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend')`).Scan(&maxConn, &reserved, &used); err != nil {
		return err
	}
	target := int(0.92*float64(maxConn-reserved)) - used
	if target <= 0 {
		return fmt.Errorf("already %d of %d available connections in use", used, maxConn-reserved)
	}
	cfg, err := pgx.ParseConfig(c.DSN)
	if err != nil {
		return errors.New("invalid lab DSN")
	}
	cfg.RuntimeParams["application_name"] = AppPrefix + "pressure"
	var conns []*pgx.Conn
	defer func() {
		for _, cn := range conns {
			_ = cn.Close(context.Background())
		}
		logf(c, "closed %d connections", len(conns))
	}()
	for i := 0; i < target; i++ {
		cn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return fmt.Errorf("open connection %d of %d: %w", i+1, target, err)
		}
		conns = append(conns, cn)
	}
	logf(c, "opened %d idle connections (%d were already in use; %d available slots)", len(conns), used, maxConn-reserved)
	hold(ctx, c, d, "connection pressure")
	return nil
}

const regressionQuery = "SELECT count(*), coalesce(sum(amount), 0) FROM lab_orders WHERE customer_id = $1"

func runRegression(ctx context.Context, c Config, d time.Duration) error {
	if err := Setup(ctx, c); err != nil {
		return err
	}
	p, err := pool(ctx, c.DSN, "regression", 2)
	if err != nil {
		return err
	}
	defer p.Close()
	defer func() {
		_, err := p.Exec(context.Background(), "CREATE INDEX IF NOT EXISTS "+ordersIndex+" ON lab_orders (customer_id)")
		if err != nil {
			logf(c, "restore index failed: %v (run `locklens-lab stop`)", err)
		} else {
			logf(c, "index %s restored", ordersIndex)
		}
	}()
	if _, err := p.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+ordersIndex+" ON lab_orders (customer_id)"); err != nil {
		return err
	}
	run := func(ctx context.Context) error {
		var n int64
		var s float64
		return p.QueryRow(ctx, regressionQuery, rand.IntN(5000)+1).Scan(&n, &s)
	}
	phase := d / 2
	logf(c, "phase 1: query with index for %s", phase)
	c1, cancel1 := context.WithTimeout(ctx, phase)
	err = pace(c1, 8, run)
	cancel1()
	if err != nil || ctx.Err() != nil {
		return err
	}
	if _, err := p.Exec(ctx, "DROP INDEX IF EXISTS "+ordersIndex); err != nil {
		return err
	}
	logf(c, "phase 2: index dropped; the same query now scans the table for %s", d-phase)
	c2, cancel2 := context.WithTimeout(ctx, d-phase)
	defer cancel2()
	return pace(c2, 8, run)
}

func runTempFiles(ctx context.Context, c Config, d time.Duration) error {
	if err := Setup(ctx, c); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	p, err := pool(ctx, c.DSN, "temp-files", 1)
	if err != nil {
		return err
	}
	defer p.Close()
	logf(c, "sorting with work_mem=64kB every 2 s for %s", d)
	return pace(ctx, 0.5, func(ctx context.Context) error {
		return pgx.BeginFunc(ctx, p, func(tx pgx.Tx) error {
			if _, err := tx.Exec(ctx, "SET LOCAL work_mem = '64kB'"); err != nil {
				return err
			}
			var id int64
			return tx.QueryRow(ctx, "SELECT id FROM lab_orders ORDER BY note, amount OFFSET 150000 LIMIT 1").Scan(&id)
		})
	})
}

func runReplication(ctx context.Context, c Config, d time.Duration) error {
	if c.StandbyAdminDSN == "" {
		return errors.New("LOCKLENS_LAB_STANDBY_ADMIN_DSN is required for the replication scenario")
	}
	admin, err := pool(ctx, c.AdminDSN, "replication-admin", 2)
	if err != nil {
		return err
	}
	defer admin.Close()
	standby, err := pool(ctx, c.StandbyAdminDSN, "replication-admin", 1)
	if err != nil {
		return err
	}
	defer standby.Close()
	defer func() {
		cctx := context.Background()
		_, e1 := standby.Exec(cctx, "SELECT pg_wal_replay_resume()")
		_, e2 := admin.Exec(cctx, "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = $1", staleSlot)
		_, e3 := admin.Exec(cctx, "DROP TABLE IF EXISTS "+walChurnTable)
		if err := errors.Join(e1, e2, e3); err != nil {
			logf(c, "cleanup note: %v (run `locklens-lab stop`)", err)
		} else {
			logf(c, "replay resumed, slot %s dropped, churn table removed", staleSlot)
		}
	}()
	if _, err := admin.Exec(ctx, "SELECT pg_create_physical_replication_slot($1, true) WHERE NOT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", staleSlot); err != nil {
		return err
	}
	if _, err := standby.Exec(ctx, "SELECT pg_wal_replay_pause()"); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE IF NOT EXISTS "+walChurnTable+" (id bigserial, payload text)"); err != nil {
		return err
	}
	logf(c, "slot %s created, standby replay paused; writing about 2 MiB of WAL per second for %s", staleSlot, d)
	wctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	i := 0
	return pace(wctx, 2, func(ctx context.Context) error {
		i++
		if i%60 == 0 {
			if _, err := admin.Exec(ctx, "TRUNCATE "+walChurnTable); err != nil {
				return err
			}
		}
		_, err := admin.Exec(ctx, "INSERT INTO "+walChurnTable+" (payload) SELECT repeat(md5(random()::text), 32) FROM generate_series(1, 1000)")
		return err
	})
}

// Status prints what the lab currently has running.
func Status(ctx context.Context, c Config) error {
	admin, err := pool(ctx, c.AdminDSN, "status", 1)
	if err != nil {
		return err
	}
	defer admin.Close()
	rows, err := admin.Query(ctx, `SELECT application_name, count(*), max(clock_timestamp() - backend_start)::text
FROM pg_stat_activity WHERE application_name LIKE $1 AND pid <> pg_backend_pid() GROUP BY 1 ORDER BY 1`, AppPrefix+"%")
	if err != nil {
		return err
	}
	var app, age string
	var n int
	found := false
	_, err = pgx.ForEachRow(rows, []any{&app, &n, &age}, func() error {
		found = true
		logf(c, "sessions: %-32s %3d (oldest %s)", app, n, age)
		return nil
	})
	if err != nil {
		return err
	}
	if !found {
		logf(c, "sessions: none")
	}
	var slot bool
	if err := admin.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $1)", staleSlot).Scan(&slot); err != nil {
		return err
	}
	var index bool
	if err := admin.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", ordersIndex).Scan(&index); err != nil {
		return err
	}
	logf(c, "slot %s present: %t; index %s present: %t", staleSlot, slot, ordersIndex, index)
	if c.StandbyAdminDSN != "" {
		if sb, err := pool(ctx, c.StandbyAdminDSN, "status", 1); err == nil {
			var state string
			if err := sb.QueryRow(ctx, "SELECT pg_get_wal_replay_pause_state()").Scan(&state); err == nil {
				logf(c, "standby replay: %s", state)
			}
			sb.Close()
		}
	}
	return nil
}

// Stop terminates lab sessions and undoes lab changes made by any runner.
func Stop(ctx context.Context, c Config) error {
	admin, err := pool(ctx, c.AdminDSN, "stop", 1)
	if err != nil {
		return err
	}
	defer admin.Close()
	var errs []error
	var n int
	if err := admin.QueryRow(ctx, `SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity
WHERE application_name LIKE $1 AND pid <> pg_backend_pid()`, AppPrefix+"%").Scan(&n); err != nil {
		errs = append(errs, err)
	}
	logf(c, "terminated %d lab session(s)", n)
	if _, err := admin.Exec(ctx, "SELECT pg_drop_replication_slot(slot_name) FROM pg_replication_slots WHERE slot_name = $1 AND NOT active", staleSlot); err != nil {
		errs = append(errs, err)
	}
	if _, err := admin.Exec(ctx, "DROP TABLE IF EXISTS "+walChurnTable); err != nil {
		errs = append(errs, err)
	}
	var hasOrders bool
	if err := admin.QueryRow(ctx, "SELECT to_regclass('lab_orders') IS NOT NULL").Scan(&hasOrders); err == nil && hasOrders {
		if _, err := admin.Exec(ctx, "CREATE INDEX IF NOT EXISTS "+ordersIndex+" ON lab_orders (customer_id)"); err != nil {
			errs = append(errs, err)
		}
	}
	if c.StandbyAdminDSN != "" {
		if sb, err := pool(ctx, c.StandbyAdminDSN, "stop", 1); err == nil {
			if _, err := sb.Exec(ctx, "SELECT pg_wal_replay_resume()"); err != nil && !strings.Contains(err.Error(), "not in recovery") {
				errs = append(errs, err)
			}
			sb.Close()
		} else {
			errs = append(errs, err)
		}
	}
	logf(c, "lab cleanup done")
	return errors.Join(errs...)
}
