package repository

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// Server is a LockLens server sharing this store.
type Server struct {
	ID          string    `json:"id"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Alive       bool      `json:"alive"`
}

// Lease is a lease on a target's collection or a job.
type Lease struct {
	Name       string    `json:"name"`
	Holder     string    `json:"holder"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
	Live       bool      `json:"live"`
}

// Heartbeat registers a server or renews its heartbeat.
func (s *Store) Heartbeat(ctx context.Context, sv Server) error {
	_, err := s.pool.Exec(ctx, queries.Heartbeat, sv.ID, sv.Hostname, sv.Version, sv.StartedAt.UTC())
	return s.red.Error(err)
}

// Servers returns the known servers; Alive is a heartbeat within ttl.
func (s *Store) Servers(ctx context.Context, ttl time.Duration) ([]Server, error) {
	rows, err := s.pool.Query(ctx, queries.LiveServers, ttl.Seconds())
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Server, error) {
		var x Server
		err := r.Scan(&x.ID, &x.Hostname, &x.Version, &x.StartedAt, &x.HeartbeatAt, &x.Alive)
		return x, err
	})
	return out, s.red.Error(err)
}

// HoldLease takes or renews a lease for ttl; false while another holder has
// it. acquired is when the holder took it (to tell a new lease from a
// renewal).
func (s *Store) HoldLease(ctx context.Context, name, holder string, ttl time.Duration) (held bool, acquired time.Time, err error) {
	err = s.pool.QueryRow(ctx, queries.HoldLease, name, holder, ttl.Seconds()).Scan(&acquired)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, time.Time{}, nil
	}
	return err == nil, acquired, s.red.Error(err)
}

// ReleaseLease gives up a lease the holder has.
func (s *Store) ReleaseLease(ctx context.Context, name, holder string) error {
	_, err := s.pool.Exec(ctx, queries.ReleaseLease, name, holder)
	return s.red.Error(err)
}

// Leases returns the leases whose names start with prefix.
func (s *Store) Leases(ctx context.Context, prefix string) ([]Lease, error) {
	rows, err := s.pool.Query(ctx, queries.ListLeases, prefix)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (Lease, error) {
		var x Lease
		err := r.Scan(&x.Name, &x.Holder, &x.AcquiredAt, &x.ExpiresAt, &x.Live)
		return x, err
	})
	return out, s.red.Error(err)
}

// SaveLiveState publishes a target's live view for the other servers.
func (s *Store) SaveLiveState(ctx context.Context, targetID int64, serverID string, at time.Time, view []byte) error {
	_, err := s.pool.Exec(ctx, queries.SaveLiveState, targetID, serverID, at.UTC(), view)
	return s.red.Error(err)
}

// LiveState returns a target's published live view.
func (s *Store) LiveState(ctx context.Context, targetID int64) (serverID string, at time.Time, view []byte, err error) {
	err = s.pool.QueryRow(ctx, queries.LiveState, targetID).Scan(&serverID, &at, &view)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", time.Time{}, nil, ErrNotFound
	}
	return serverID, at, view, s.red.Error(err)
}

// Notify sends a message to every server listening on a channel.
func (s *Store) Notify(ctx context.Context, channel, payload string) error {
	_, err := s.pool.Exec(ctx, queries.Notify, channel, payload)
	return s.red.Error(err)
}

// Listen calls fn with every message on a channel until ctx ends. It holds
// one pooled connection; on a connection error it returns the error (the
// caller listens again).
func (s *Store) Listen(ctx context.Context, channel string, fn func(payload string)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return s.red.Error(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, queries.Listen(channel)); err != nil {
		return s.red.Error(err)
	}
	defer func() {
		// The connection returns to the pool: stop listening on it.
		uctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(uctx, queries.UnlistenAll); err != nil {
			conn.Conn().Close(uctx)
		}
	}()
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return s.red.Error(err)
		}
		fn(n.Payload)
	}
}
