package targetdb

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

func ReadReplication(ctx context.Context, tx pgx.Tx, caps observe.Capabilities) (observe.Replication, error) {
	r := observe.Replication{At: time.Now().UTC(), Role: "primary", Replicas: []observe.Replica{}, Slots: []observe.Slot{}, Warnings: []string{}}
	if caps.IsStandby {
		r.Role = "standby"
	}
	if !caps.IsStandby {
		var lsn string
		if err := tx.QueryRow(ctx, queries.CurrentWALLSN).Scan(&lsn); err != nil {
			return r, err
		}
		r.CurrentLSN = &lsn
		rows, err := tx.Query(ctx, queries.Replicas)
		if err != nil {
			return r, err
		}
		r.Replicas, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (observe.Replica, error) {
			var x observe.Replica
			err := row.Scan(&x.PID, &x.UserName, &x.ApplicationName, &x.ClientAddr, &x.State, &x.SyncState,
				&x.SentLSN, &x.WriteLSN, &x.FlushLSN, &x.ReplayLSN, &x.SentLagBytes, &x.WriteLagBytes,
				&x.FlushLagBytes, &x.ReplayLagBytes, &x.WriteLagSec, &x.FlushLagSec, &x.ReplayLagSec, &x.ReplyTime)
			return x, err
		})
		if err != nil {
			return r, err
		}
		if r.Replicas == nil {
			r.Replicas = []observe.Replica{}
		}
		if !caps.ReadAllStats && len(r.Replicas) > 0 {
			r.Warnings = append(r.Warnings, "replica LSN positions may be hidden: the monitoring role lacks pg_read_all_stats")
		}
	} else {
		var s observe.Standby
		if err := tx.QueryRow(ctx, queries.Standby).Scan(&s.ReceiveLSN, &s.ReplayLSN, &s.ReplayDelayBytes, &s.LastReplayTime, &s.ReplayPauseState); err != nil {
			return r, err
		}
		err := tx.QueryRow(ctx, queries.WalReceiver).Scan(&s.ReceiverStatus, &s.SenderHost, &s.ReceiverSlot, &s.LastMessageReceipt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return r, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			r.Warnings = append(r.Warnings, "no WAL receiver is running (archive recovery or the receiver is restarting)")
		}
		r.Standby = &s
	}

	rows, err := tx.Query(ctx, queries.Slots)
	if err != nil {
		return r, err
	}
	r.Slots, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (observe.Slot, error) {
		var s observe.Slot
		err := row.Scan(&s.Name, &s.Type, &s.Active, &s.WALStatus, &s.RestartLSN, &s.RetainedBytes, &s.SafeWALSize)
		return s, err
	})
	if err != nil {
		return r, err
	}
	if r.Slots == nil {
		r.Slots = []observe.Slot{}
	}
	if caps.VersionNum >= 170000 && len(r.Slots) > 0 {
		rows, err := tx.Query(ctx, queries.SlotsInactiveSince)
		if err != nil {
			return r, err
		}
		since := map[string]*time.Time{}
		var name string
		var ts *time.Time
		if _, err := pgx.ForEachRow(rows, []any{&name, &ts}, func() error { since[name] = ts; return nil }); err != nil {
			return r, err
		}
		for i := range r.Slots {
			r.Slots[i].InactiveSince = since[r.Slots[i].Name]
		}
	}

	var a observe.Archiver
	if err := tx.QueryRow(ctx, queries.Archiver).Scan(&a.ArchiveMode, &a.ArchivedCount, &a.FailedCount,
		&a.LastArchivedTime, &a.LastFailedTime, &a.LastFailedWAL); err != nil {
		return r, err
	}
	r.Archiver = &a
	return r, nil
}
