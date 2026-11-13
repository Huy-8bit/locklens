package repository

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// ClusterRepository is the store of the servers sharing the store, leases, published live views, and the message bus.
type ClusterRepository interface {
	// Heartbeat registers a server or renews its heartbeat.
	Heartbeat(ctx context.Context, sv Server) error
	// Servers returns the known servers; Alive is a heartbeat within ttl.
	Servers(ctx context.Context, ttl time.Duration) ([]Server, error)
	// HoldLease takes or renews a lease for ttl; false while another holder has
	// it. acquired is when the holder took it (to tell a new lease from a
	// renewal).
	HoldLease(ctx context.Context, name, holder string, ttl time.Duration) (held bool, acquired time.Time, err error)
	// ReleaseLease gives up a lease the holder has.
	ReleaseLease(ctx context.Context, name, holder string) error
	// HoldLeases takes or renews many leases in one statement (as HoldLease)
	// and returns the names the holder has afterwards.
	HoldLeases(ctx context.Context, names []string, holder string, ttl time.Duration) ([]string, error)
	// ReleaseLeases gives up many leases the holder has in one statement.
	ReleaseLeases(ctx context.Context, names []string, holder string) error
	// Leases returns the leases whose names start with prefix.
	Leases(ctx context.Context, prefix string) ([]Lease, error)
	// SaveLiveState publishes a target's live view for the other servers.
	SaveLiveState(ctx context.Context, targetID int64, serverID string, at time.Time, view []byte) error
	// LiveState returns a target's published live view.
	LiveState(ctx context.Context, targetID int64) (serverID string, at time.Time, view []byte, err error)
	// LiveStates returns the published live views of many targets in one
	// statement (targets without one are absent).
	LiveStates(ctx context.Context, targetIDs []int64) ([]LiveState, error)
	// Notify sends a message to every server listening on a channel.
	Notify(ctx context.Context, channel, payload string) error
	// Listen calls fn with every message on a channel until ctx ends. It holds
	// one pooled connection; on a connection error it returns the error (the
	// caller listens again).
	Listen(ctx context.Context, channel string, fn func(payload string)) error
}

var _ ClusterRepository = (*Store)(nil)

// Server is a LockLens server sharing this store.
type Server struct {
	ID          string    `json:"id"`
	Hostname    string    `json:"hostname"`
	Version     string    `json:"version"`
	StartedAt   time.Time `json:"started_at"`
	HeartbeatAt time.Time `json:"heartbeat_at"`
	Alive       bool      `json:"alive"`
}

// LiveState is a target's live view as published by the server collecting it.
type LiveState struct {
	TargetID int64
	ServerID string
	At       time.Time
	View     []byte
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

// HoldLeases takes or renews many leases in one statement (as HoldLease)
// and returns the names the holder has afterwards. Names are sorted first, so
// servers holding overlapping sets lock the rows in the same order.
func (s *Store) HoldLeases(ctx context.Context, names []string, holder string, ttl time.Duration) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	sorted := slices.Clone(names)
	slices.Sort(sorted)
	rows, err := s.pool.Query(ctx, queries.HoldLeases, sorted, holder, ttl.Seconds())
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	return out, s.red.Error(err)
}

// ReleaseLeases gives up many leases the holder has in one statement.
func (s *Store) ReleaseLeases(ctx context.Context, names []string, holder string) error {
	if len(names) == 0 {
		return nil
	}
	_, err := s.pool.Exec(ctx, queries.ReleaseLeases, names, holder)
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

// LiveStates returns the published live views of many targets in one
// statement (targets without one are absent).
func (s *Store) LiveStates(ctx context.Context, targetIDs []int64) ([]LiveState, error) {
	if len(targetIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, queries.LiveStates, targetIDs)
	if err != nil {
		return nil, s.red.Error(err)
	}
	out, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (LiveState, error) {
		var x LiveState
		err := r.Scan(&x.TargetID, &x.ServerID, &x.At, &x.View)
		return x, err
	})
	return out, s.red.Error(err)
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
