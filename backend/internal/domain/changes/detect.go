package changes

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/repository"
)

// State kinds: the last reading each detection compares against.
const (
	stateServer = "server"
	statePrefix = "schema:" // + database name
)

// ObserveServer compares a server reading with the previous one, records
// the changes, and keeps the reading. The first reading only sets the
// baseline.
func ObserveServer(ctx context.Context, s repository.ChangeRepository, targetID int64, cur observe.ServerState) error {
	return observe1(ctx, s, targetID, stateServer, cur, DiffServer)
}

// ObserveSchema does the same for the schema of one database.
func ObserveSchema(ctx context.Context, s repository.ChangeRepository, targetID int64, c observe.Catalog) error {
	return observe1(ctx, s, targetID, statePrefix+c.DatName, SchemaOf(c), DiffSchema)
}

func observe1[T any](ctx context.Context, s repository.ChangeRepository, targetID int64, kind string, cur T, diff func(prev, cur T) []Event) error {
	raw, err := s.ChangeState(ctx, targetID, kind)
	switch {
	case errors.Is(err, repository.ErrNotFound):
	case err != nil:
		return err
	default:
		var prev T
		if err := json.Unmarshal(raw, &prev); err == nil {
			events := diff(prev, cur)
			for i := range events {
				events[i].TargetID = &targetID
			}
			if len(events) > 0 {
				if err := s.InsertChanges(ctx, events); err != nil {
					return err
				}
			}
		}
	}
	next, err := json.Marshal(cur)
	if err != nil {
		return err
	}
	return s.SaveChangeState(ctx, targetID, kind, time.Now().UTC(), next)
}
