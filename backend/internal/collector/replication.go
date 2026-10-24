package collector

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/observe"
)

// Replicas of a primary. LSN columns are NULL for roles without
// pg_read_all_stats. *_lag columns are measured by the primary from standby
// feedback and become NULL when the standby has caught up and is idle
// (PostgreSQL documents this), so byte lags are reported alongside.
const sqlReplicas = `
SELECT r.pid, r.usename, r.application_name, host(r.client_addr), r.state, r.sync_state,
       r.sent_lsn::text, r.write_lsn::text, r.flush_lsn::text, r.replay_lsn::text,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.sent_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.write_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.flush_lsn)::float8,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_current_wal_lsn(), r.replay_lsn)::float8,
       extract(epoch FROM r.write_lag)::float8, extract(epoch FROM r.flush_lag)::float8,
       extract(epoch FROM r.replay_lag)::float8, r.reply_time
FROM pg_catalog.pg_stat_replication AS r
ORDER BY r.application_name, r.pid`

// Replication slots and the WAL each one retains (distance from restart_lsn
// to the current or received position). safe_wal_size is NULL unless
// max_slot_wal_keep_size is set.
const sqlSlots = `
SELECT s.slot_name, s.slot_type, s.active, s.wal_status, s.restart_lsn::text,
       pg_catalog.pg_wal_lsn_diff(
           CASE WHEN pg_catalog.pg_is_in_recovery() THEN pg_catalog.pg_last_wal_receive_lsn()
                ELSE pg_catalog.pg_current_wal_lsn() END, s.restart_lsn)::float8,
       s.safe_wal_size
FROM pg_catalog.pg_replication_slots AS s
ORDER BY s.slot_name`

// inactive_since exists from PostgreSQL 17.
const sqlSlotsInactiveSince = `SELECT slot_name, inactive_since FROM pg_catalog.pg_replication_slots`

// Standby status. pg_last_xact_replay_timestamp() is the commit time of the
// last replayed transaction: when the primary is idle it grows without any
// real lag, so LockLens never presents it as replication lag.
const sqlStandby = `
SELECT pg_catalog.pg_last_wal_receive_lsn()::text, pg_catalog.pg_last_wal_replay_lsn()::text,
       pg_catalog.pg_wal_lsn_diff(pg_catalog.pg_last_wal_receive_lsn(), pg_catalog.pg_last_wal_replay_lsn())::float8,
       pg_catalog.pg_last_xact_replay_timestamp(), pg_catalog.pg_get_wal_replay_pause_state()`

// Columns other than pid and status are NULL without pg_read_all_stats.
const sqlWalReceiver = `
SELECT status, sender_host, slot_name, last_msg_receipt_time FROM pg_catalog.pg_stat_wal_receiver`

const sqlArchiver = `
SELECT current_setting('archive_mode'), archived_count, failed_count, last_archived_time,
       last_failed_time, last_failed_wal
FROM pg_catalog.pg_stat_archiver`

func readReplication(ctx context.Context, tx pgx.Tx, caps observe.Capabilities) (observe.Replication, error) {
	r := observe.Replication{At: time.Now().UTC(), Role: "primary", Replicas: []observe.Replica{}, Slots: []observe.Slot{}, Warnings: []string{}}
	if caps.IsStandby {
		r.Role = "standby"
	}
	if !caps.IsStandby {
		var lsn string
		if err := tx.QueryRow(ctx, "SELECT pg_catalog.pg_current_wal_lsn()::text").Scan(&lsn); err != nil {
			return r, err
		}
		r.CurrentLSN = &lsn
		rows, err := tx.Query(ctx, sqlReplicas)
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
		if err := tx.QueryRow(ctx, sqlStandby).Scan(&s.ReceiveLSN, &s.ReplayLSN, &s.ReplayDelayBytes, &s.LastReplayTime, &s.ReplayPauseState); err != nil {
			return r, err
		}
		err := tx.QueryRow(ctx, sqlWalReceiver).Scan(&s.ReceiverStatus, &s.SenderHost, &s.ReceiverSlot, &s.LastMessageReceipt)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return r, err
		}
		if errors.Is(err, pgx.ErrNoRows) {
			r.Warnings = append(r.Warnings, "no WAL receiver is running (archive recovery or the receiver is restarting)")
		}
		r.Standby = &s
	}

	rows, err := tx.Query(ctx, sqlSlots)
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
		rows, err := tx.Query(ctx, sqlSlotsInactiveSince)
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
	if err := tx.QueryRow(ctx, sqlArchiver).Scan(&a.ArchiveMode, &a.ArchivedCount, &a.FailedCount,
		&a.LastArchivedTime, &a.LastFailedTime, &a.LastFailedWAL); err != nil {
		return r, err
	}
	r.Archiver = &a
	return r, nil
}

// replicationPoints derives time series from a replication collection.
func replicationPoints(r observe.Replication) []observe.Point {
	var pts []observe.Point
	if r.Role == "primary" {
		pts = append(pts, observe.Point{Metric: "replication.replicas", Value: observe.F(float64(len(r.Replicas)))})
		var maxLag *float64
		for _, x := range r.Replicas {
			label := x.ApplicationName
			if label == "" {
				label = "pid " + itoa(x.PID)
			}
			pts = append(pts, observe.Point{Metric: "replication.replay_lag_bytes", Label: label, Value: x.ReplayLagBytes})
			if x.ReplayLagBytes != nil && (maxLag == nil || *x.ReplayLagBytes > *maxLag) {
				maxLag = x.ReplayLagBytes
			}
		}
		if len(r.Replicas) > 0 {
			pts = append(pts, observe.Point{Metric: "replication.max_replay_lag_bytes", Value: maxLag})
		}
	}
	if r.Standby != nil {
		pts = append(pts, observe.Point{Metric: "standby.replay_delay_bytes", Value: r.Standby.ReplayDelayBytes})
	}
	var maxRetained *float64
	for _, s := range r.Slots {
		pts = append(pts, observe.Point{Metric: "slots.retained_bytes", Label: s.Name, Value: s.RetainedBytes})
		if s.RetainedBytes != nil && (maxRetained == nil || *s.RetainedBytes > *maxRetained) {
			maxRetained = s.RetainedBytes
		}
	}
	if len(r.Slots) > 0 {
		pts = append(pts, observe.Point{Metric: "slots.max_retained_bytes", Value: maxRetained})
	}
	return pts
}
