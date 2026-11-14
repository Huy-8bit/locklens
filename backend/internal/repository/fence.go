package repository

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/repository/queries"
)

// Fence is one holding of a target's lease. Writes made under it are
// accepted only while the lease still has this holder and epoch: a server
// that lost a target (it stalled, and another server took the target over)
// cannot overwrite what the new holder records.
type Fence struct {
	TargetID int64
	Holder   string
	Epoch    int64
}

// LeaseName is the name of the target's lease.
func (f Fence) LeaseName() string { return "target:" + strconv.FormatInt(f.TargetID, 10) }

// ErrLeaseLost is returned by a fenced write when the lease is no longer
// held under its fence; nothing was written.
var ErrLeaseLost = errors.New("lease lost")

// FencedWrites are the writes a collecting server makes under a target's
// lease, in one transaction.
type FencedWrites interface {
	FindingRepository
	IncidentRepository
	OutboxRepository
}

// FenceRepository runs writes under a lease fence.
type FenceRepository interface {
	// Fenced runs fn in one transaction that first checks the target's lease
	// against f and share locks it, so the lease cannot change holder before
	// the transaction commits. It returns ErrLeaseLost, and writes nothing,
	// when the lease is not held under f; any error of fn rolls everything
	// back.
	Fenced(ctx context.Context, f Fence, fn func(tx FencedWrites) error) error
}

var _ FenceRepository = (*Store)(nil)

// Fenced runs fn in one transaction under a lease fence; see FenceRepository.
func (s *Store) Fenced(ctx context.Context, f Fence, fn func(tx FencedWrites) error) error {
	err := pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		var one int
		if err := tx.QueryRow(ctx, queries.CheckFence, f.LeaseName(), f.Holder, f.Epoch).Scan(&one); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrLeaseLost
			}
			return err
		}
		return fn(&Store{pool: s.pool, db: tx, red: s.red, timescale: s.timescale, rawRetention: s.rawRetention})
	})
	if errors.Is(err, ErrLeaseLost) {
		return ErrLeaseLost
	}
	return s.red.Error(err)
}

// inTx runs fn in a transaction: the fenced transaction in progress when
// the store is bound to one (no savepoint: a failure fails it all), else a
// new one.
func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	if tx, ok := s.db.(pgx.Tx); ok {
		return fn(tx)
	}
	return pgx.BeginFunc(ctx, s.db, fn)
}
