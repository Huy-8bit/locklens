package collector

import (
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/config"
	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/repository"
	"github.com/locklens/locklens/internal/targetdb"
)

// Per-database data (tables, indexes, the catalog) needs a connection to each
// database; everything else is cluster-wide and read on the main connection.
const (
	// maxDatabases bounds the databases of one instance (as the API does).
	maxDatabases = 20
	// databaseListEvery is how often the databases of scope "all" are
	// listed again.
	databaseListEvery = 5 * time.Minute
	// databaseRetry is how long a database that could not be connected to
	// is skipped.
	databaseRetry = time.Minute
)

// dbConn is a connection to one database of the instance.
type dbConn struct {
	name string
	obs  *targetdb.Collector
	// main is the connection of the target's connection string, which also
	// reads cluster-wide data.
	main bool
}

// databaseSet holds the connections to the databases other than the main one.
type databaseSet struct {
	main     string   // current_database() of the main connection, once listed
	names    []string // in scope, the main database first
	listedAt time.Time
	conns    map[string]*targetdb.Collector
	failedAt map[string]time.Time
	errors   map[string]string
}

// databases returns the connections to the databases of the target's scope,
// the main one first. Others are opened on first use, skipped for a minute
// after a failure, and closed when they leave the scope.
func (w *worker) databases(ctx context.Context) []dbConn {
	main := dbConn{name: w.target.DisplayDatabase, obs: w.obs, main: true}
	if w.target.DatabaseScope == "" || w.target.DatabaseScope == repository.DatabaseConnected {
		w.closeDatabases(nil)
		return []dbConn{main}
	}
	d := &w.dbs
	if d.conns == nil {
		d.conns, d.failedAt, d.errors = map[string]*targetdb.Collector{}, map[string]time.Time{}, map[string]string{}
	}
	if time.Since(d.listedAt) > databaseListEvery || d.names == nil {
		var listed []string
		err := w.obs.ReadOnly(ctx, func(ctx context.Context, tx pgx.Tx, _ []int32) error {
			var err error
			listed, err = targetdb.ListDatabases(ctx, tx, maxDatabases)
			return err
		})
		switch {
		case err != nil:
			w.m.log.Warn("listing databases failed", "target_id", w.target.ID, "error", sanitizeError(err))
		case len(listed) > 0:
			// The connected database is listed first: its real name (the
			// connection string may not name one).
			d.main = listed[0]
			d.names, d.listedAt = scopeDatabases(w.target, d.main, listed), time.Now()
		}
	}
	if d.main != "" {
		main.name = d.main
	}
	out := []dbConn{main}
	for _, name := range d.names {
		if name == main.name {
			continue
		}
		c, ok := d.conns[name]
		if !ok {
			if time.Since(d.failedAt[name]) < databaseRetry {
				continue
			}
			var err error
			if c, err = w.openDatabase(ctx, name); err != nil {
				d.failedAt[name], d.errors[name] = time.Now(), sanitizeError(err)
				w.m.log.Warn("database connection failed", "target_id", w.target.ID, "database", name, "error", d.errors[name])
				continue
			}
			d.conns[name] = c
			delete(d.errors, name)
		}
		out = append(out, dbConn{name: name, obs: c})
	}
	w.closeDatabases(d.names)
	return out
}

// scopeDatabases are the listed databases that the target's scope selects,
// the connected one (main) first.
func scopeDatabases(t repository.Target, main string, listed []string) []string {
	if t.DatabaseScope != repository.DatabaseList {
		return listed
	}
	var out []string
	for _, name := range listed {
		if name == main || slices.Contains(t.Databases, name) {
			out = append(out, name)
		}
	}
	return out
}

// openDatabase connects to another database with the target's credentials
// (one connection).
func (w *worker) openDatabase(ctx context.Context, name string) (*targetdb.Collector, error) {
	plain, err := w.m.box.Open(w.target.DSNCiphertext, []byte(DSNAdditionalData))
	if err != nil {
		return nil, err
	}
	cfg, red, err := config.PoolConfig(string(plain), config.Options{ConnectTimeout: 5 * time.Second, QueryTimeout: 30 * time.Second})
	if err != nil {
		return nil, err
	}
	cfg.ConnConfig.Database = name
	cfg.MaxConns = 1
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return targetdb.Connect(cctx, cfg, red, model.Tool{Name: "locklens", Version: w.m.version})
}

// closeDatabases closes the connections to databases not in keep (all of
// them for nil).
func (w *worker) closeDatabases(keep []string) {
	for name, c := range w.dbs.conns {
		if !slices.Contains(keep, name) {
			c.Close()
			delete(w.dbs.conns, name)
		}
	}
}

// scopeNames are the names of the databases in scope, given the ones
// whose catalog was just collected; ok is false while they are unknown (not
// listed yet).
func (w *worker) scopeNames(collected []string) ([]string, bool) {
	if w.target.DatabaseScope == "" || w.target.DatabaseScope == repository.DatabaseConnected {
		return collected, len(collected) > 0
	}
	return w.dbs.names, w.dbs.names != nil
}
