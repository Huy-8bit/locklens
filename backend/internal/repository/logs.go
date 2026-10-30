package repository

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository/queries"
)

// LogAgent is a log agent that sent events for a target.
type LogAgent struct {
	TokenID     int64      `json:"token_id"`
	TokenName   string     `json:"token_name"`
	Hostname    string     `json:"hostname"`
	Version     string     `json:"version"`
	Sources     []string   `json:"sources"`
	FirstSeenAt time.Time  `json:"first_seen_at"`
	LastSeenAt  time.Time  `json:"last_seen_at"`
	LastEventAt *time.Time `json:"last_event_at"`
	Received    int64      `json:"received"`
}

// LogEventFilter selects log events.
type LogEventFilter struct {
	TargetID    int64
	From, To    time.Time
	Kinds       []string
	Fingerprint string
	Limit       int
}

// LogCount is the number of events of a kind in a bucket.
type LogCount struct {
	At    time.Time `json:"t"`
	Kind  string    `json:"kind"`
	Count int64     `json:"n"`
}

// LogStatement is the logged executions of one statement.
type LogStatement struct {
	Fingerprint string    `json:"fingerprint"`
	Source      string    `json:"source"` // slow_query or plan
	Query       *string   `json:"query"`
	QueryID     *int64    `json:"query_id"`
	Count       int64     `json:"count"`
	TotalMS     float64   `json:"total_ms"`
	P50MS       float64   `json:"p50_ms"`
	P95MS       float64   `json:"p95_ms"`
	P99MS       float64   `json:"p99_ms"`
	MaxMS       float64   `json:"max_ms"`
	LastAt      time.Time `json:"last_at"`
	Plans       int64     `json:"plans"`
}

// InsertLogEvents stores log events of a target (ones already stored are
// skipped) and records the agent that sent them. It returns how many were
// new.
func (s *Store) InsertLogEvents(ctx context.Context, targetID, tokenID int64, hostname, version string, sources []string, events []observe.LogEvent) (int64, error) {
	var inserted int64
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		var last *time.Time
		for _, e := range events {
			details, err := json.Marshal(e.Details)
			if err != nil {
				return err
			}
			if e.Details == nil {
				details = []byte("{}")
			}
			batch.Queue(queries.InsertLogEvent, targetID, e.At.UTC(), e.ID, e.Kind, nullInt(e.PID), nullStr(e.DatName), nullStr(e.UserName),
				nullStr(e.Application), e.Severity, nullStr(e.SQLState), e.DurationMS, e.QueryID, nullStr(e.Fingerprint), nullStr(e.Query),
				e.Message, details)
			if at := e.At.UTC(); last == nil || at.After(*last) {
				last = &at
			}
		}
		if batch.Len() > 0 {
			br := tx.SendBatch(ctx, batch)
			for range batch.Len() {
				tag, err := br.Exec()
				if err != nil {
					br.Close()
					return err
				}
				inserted += tag.RowsAffected()
			}
			if err := br.Close(); err != nil {
				return err
			}
		}
		if sources == nil {
			sources = []string{}
		}
		src, err := json.Marshal(sources)
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, queries.UpsertLogAgent, tokenID, targetID, hostname, version, src, last, inserted)
		return err
	})
	return inserted, s.red.Error(err)
}

// LogAgents returns the live log agents of a target.
func (s *Store) LogAgents(ctx context.Context, targetID int64) ([]LogAgent, error) {
	rows, err := s.pool.Query(ctx, queries.ListLogAgents, targetID)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogAgent, error) {
		var a LogAgent
		var src []byte
		if err := r.Scan(&a.TokenID, &a.TokenName, &a.Hostname, &a.Version, &src, &a.FirstSeenAt, &a.LastSeenAt, &a.LastEventAt, &a.Received); err != nil {
			return a, err
		}
		return a, json.Unmarshal(src, &a.Sources)
	})
	if out == nil {
		out = []LogAgent{}
	}
	return out, s.red.Error(err)
}

// LogEvents returns log events, the newest first.
func (s *Store) LogEvents(ctx context.Context, f LogEventFilter) ([]observe.LogEvent, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	if f.Kinds == nil {
		f.Kinds = []string{}
	}
	rows, err := s.pool.Query(ctx, queries.ListLogEvents, f.TargetID, f.From.UTC(), f.To.UTC(), f.Kinds, f.Fingerprint, f.Limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (observe.LogEvent, error) {
		var e observe.LogEvent
		var pid *int
		var datname, user, app, sqlstate, fp, query *string
		var details []byte
		if err := r.Scan(&e.At, &e.ID, &e.Kind, &pid, &datname, &user, &app, &e.Severity, &sqlstate, &e.DurationMS, &e.QueryID, &fp,
			&query, &e.Message, &details); err != nil {
			return e, err
		}
		e.At = e.At.UTC()
		if pid != nil {
			e.PID = *pid
		}
		e.DatName, e.UserName, e.Application, e.SQLState, e.Fingerprint, e.Query = deref(datname), deref(user), deref(app), deref(sqlstate), deref(fp), deref(query)
		return e, json.Unmarshal(details, &e.Details)
	})
	if out == nil {
		out = []observe.LogEvent{}
	}
	return out, s.red.Error(err)
}

// LogCounts returns the events per kind in buckets of step.
func (s *Store) LogCounts(ctx context.Context, targetID int64, from, to time.Time, step time.Duration) ([]LogCount, error) {
	rows, err := s.pool.Query(ctx, queries.LogCounts, targetID, from.UTC(), to.UTC(), step.Seconds())
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogCount, error) {
		var c LogCount
		err := r.Scan(&c.At, &c.Kind, &c.Count)
		c.At = c.At.UTC()
		return c, err
	})
	if out == nil {
		out = []LogCount{}
	}
	return out, s.red.Error(err)
}

// LogStatements returns the statements with the most logged execution
// time.
func (s *Store) LogStatements(ctx context.Context, targetID int64, from, to time.Time, limit int) ([]LogStatement, error) {
	rows, err := s.pool.Query(ctx, queries.LogStatements, targetID, from.UTC(), to.UTC(), limit)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LogStatement, error) {
		var x LogStatement
		err := r.Scan(&x.Fingerprint, &x.Source, &x.Query, &x.QueryID, &x.Count, &x.TotalMS, &x.P50MS, &x.P95MS, &x.P99MS, &x.MaxMS, &x.LastAt, &x.Plans)
		x.LastAt = x.LastAt.UTC()
		return x, err
	})
	if out == nil {
		out = []LogStatement{}
	}
	return out, s.red.Error(err)
}

func nullStr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func nullInt(n int) *int {
	if n == 0 {
		return nil
	}
	return &n
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
