package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
)

// CollectionRepository is the store of writes of collected data: metric points, statements, snapshots, table health, gaps.
type CollectionRepository interface {
	// InsertPoints writes time-series points sharing one timestamp. Duplicate
	// (target, metric, label, ts) rows are ignored so retried batches are safe.
	InsertPoints(ctx context.Context, targetID int64, ts time.Time, points []observe.Point) error
	// InsertStatements stores one pg_stat_statements collection: identities are
	// upserted (text only when includeText), deltas inserted as samples, and a
	// statement_collections row records coverage and resets.
	InsertStatements(ctx context.Context, targetID int64, c observe.StatementsCollection, includeText bool, cache IdentityCache) error
	// ClearQueryTexts removes stored statement texts of a target, used when the
	// target's query-text collection is switched off.
	ClearQueryTexts(ctx context.Context, targetID int64) error
	// InsertActivitySnapshot stores a blocking snapshot.
	InsertActivitySnapshot(ctx context.Context, targetID int64, ts time.Time, blocked, roots, longTx int, snapshot []byte) error
	// InsertTableHealth stores table and index statistics.
	InsertTableHealth(ctx context.Context, targetID int64, h observe.TableHealth) error
	// InsertStateSnapshot stores a JSON document of the given kind.
	InsertStateSnapshot(ctx context.Context, targetID int64, kind string, ts time.Time, payload []byte) error
	// InsertGap records a period without data for a collector.
	InsertGap(ctx context.Context, targetID int64, collector string, from, to time.Time, reason string) error
}

var _ CollectionRepository = (*Store)(nil)

// InsertPoints writes time-series points sharing one timestamp. Duplicate
// (target, metric, label, ts) rows are ignored so retried batches are safe.
func (s *Store) InsertPoints(ctx context.Context, targetID int64, ts time.Time, points []observe.Point) error {
	if len(points) == 0 {
		return nil
	}
	metrics := make([]string, len(points))
	labels := make([]string, len(points))
	values := make([]*float64, len(points))
	intervals := make([]*float64, len(points))
	for i, p := range points {
		metrics[i], labels[i], values[i], intervals[i] = p.Metric, p.Label, p.Value, p.IntervalSeconds
	}
	_, err := s.pool.Exec(ctx, queries.InsertPoints, targetID, ts.UTC(), metrics, labels, values, intervals)
	return s.red.Error(err)
}

// IdentityCache maps query keys to identity IDs. It is owned by the single
// writer goroutine and needs no locking.
type IdentityCache map[int64]map[observe.QueryKey]int64

// InsertStatements stores one pg_stat_statements collection: identities are
// upserted (text only when includeText), deltas inserted as samples, and a
// statement_collections row records coverage and resets.
func (s *Store) InsertStatements(ctx context.Context, targetID int64, c observe.StatementsCollection, includeText bool, cache IdentityCache) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ids := cache[targetID]
		if ids == nil {
			ids = map[observe.QueryKey]int64{}
			cache[targetID] = ids
		}
		var (
			majors              []int32
			dbids, userids      []int64
			toplevels           []bool
			queryids            []int64
			datnames, usernames []string
			texts               []*string
			commands            []string
		)
		for _, d := range c.Deltas {
			text := d.Text
			if !includeText {
				text = nil
			}
			majors = append(majors, int32(d.Key.ServerMajor))
			dbids = append(dbids, int64(d.Key.DBID))
			userids = append(userids, int64(d.Key.UserID))
			toplevels = append(toplevels, d.Key.TopLevel)
			queryids = append(queryids, d.Key.QueryID)
			datnames = append(datnames, d.DatName)
			usernames = append(usernames, d.UserName)
			texts = append(texts, text)
			cmd := d.Command
			if cmd == "" {
				cmd = observe.CommandOther
			}
			commands = append(commands, cmd)
		}
		if len(c.Deltas) > 0 {
			rows, err := tx.Query(ctx, queries.UpsertQueryIdentity,
				targetID, majors, dbids, userids, toplevels, queryids, datnames, usernames, texts, c.At.UTC(), includeText, commands)
			if err != nil {
				return err
			}
			var (
				id        int64
				k         observe.QueryKey
				dbid, uid uint32
			)
			_, err = pgx.ForEachRow(rows, []any{&id, &k.ServerMajor, &dbid, &uid, &k.TopLevel, &k.QueryID}, func() error {
				k.DBID, k.UserID = dbid, uid
				ids[k] = id
				return nil
			})
			if err != nil {
				return err
			}
			if c.Interval != nil {
				var (
					idList                         []int64
					calls, rowsN, hit, read, tempW []int64
					total                          []float64
					wal                            []*float64
				)
				for _, d := range c.Deltas {
					idList = append(idList, ids[d.Key])
					calls = append(calls, d.Calls)
					total = append(total, d.TotalMS)
					rowsN = append(rowsN, d.Rows)
					hit = append(hit, d.BlkHit)
					read = append(read, d.BlkRead)
					tempW = append(tempW, d.TempW)
					wal = append(wal, d.WAL)
				}
				if _, err := tx.Exec(ctx, queries.InsertQuerySample, targetID, c.At.UTC(), *c.Interval, idList, calls, total, rowsN, hit, read, tempW, wal); err != nil {
					return err
				}
			}
		}
		_, err := tx.Exec(ctx, queries.InsertStatementCollection,
			targetID, c.At.UTC(), c.Interval, c.Entries, len(c.Deltas)+c.Truncated, c.Hidden, c.Dealloc, c.Reset)
		return err
	})
}

// ClearQueryTexts removes stored statement texts of a target, used when the
// target's query-text collection is switched off.
func (s *Store) ClearQueryTexts(ctx context.Context, targetID int64) error {
	_, err := s.pool.Exec(ctx, queries.ClearQueryTexts, targetID)
	return s.red.Error(err)
}

// InsertActivitySnapshot stores a blocking snapshot.
func (s *Store) InsertActivitySnapshot(ctx context.Context, targetID int64, ts time.Time, blocked, roots, longTx int, snapshot []byte) error {
	_, err := s.pool.Exec(ctx, queries.InsertActivitySnapshot, targetID, ts.UTC(), blocked, roots, longTx, snapshot)
	return s.red.Error(err)
}

// InsertTableHealth stores table and index statistics.
func (s *Store) InsertTableHealth(ctx context.Context, targetID int64, h observe.TableHealth) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		ts := h.At.UTC()
		batch := &pgx.Batch{}
		for _, t := range h.Tables {
			batch.Queue(queries.InsertTableStat, targetID, ts, t.RelID, t.DatName, t.Schema, t.Name, t.LiveTuples, t.DeadTuples,
				t.ModSinceAnalyze, t.LastVacuum, t.LastAutovacuum, t.LastAnalyze, t.LastAutoanalyze, t.VacuumCount,
				t.AutovacuumCount, t.AnalyzeCount, t.AutoanalyzeCount, t.TableBytes, t.IndexesBytes, t.TotalBytes, t.FrozenXIDAge)
		}
		for _, i := range h.Indexes {
			batch.Queue(queries.InsertIndexStat,
				targetID, ts, i.IndexRelID, i.RelID, i.DatName, i.Schema, i.Table, i.Name, i.Scans, i.Bytes, i.IsUnique, i.IsPrimary, i.IsConstraint)
		}
		if batch.Len() > 0 {
			if err := tx.SendBatch(ctx, batch).Close(); err != nil {
				return err
			}
		}
		meta := map[string]any{"datname": h.DatName, "size_source": h.SizeSource, "databases": h.Databases,
			"settings": h.Settings, "warnings": h.Warnings}
		raw, err := json.Marshal(meta)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, queries.InsertTableHealthState, targetID, ts, raw)
		return err
	})
}

// InsertStateSnapshot stores a JSON document of the given kind.
func (s *Store) InsertStateSnapshot(ctx context.Context, targetID int64, kind string, ts time.Time, payload []byte) error {
	_, err := s.pool.Exec(ctx, queries.InsertStateSnapshot, targetID, kind, ts.UTC(), payload)
	return s.red.Error(err)
}

// InsertGap records a period without data for a collector.
func (s *Store) InsertGap(ctx context.Context, targetID int64, collector string, from, to time.Time, reason string) error {
	_, err := s.pool.Exec(ctx, queries.InsertGap, targetID, collector, from.UTC(), to.UTC(), reason)
	return s.red.Error(err)
}
