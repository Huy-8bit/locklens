package changes

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

// Limits on reported changes.
const (
	maxTitle      = 200
	maxDetails    = 16 << 10
	maxReportedIn = 30 * 24 * time.Hour // how far back a change may be reported
)

// Service records reported changes and lists all of them.
type Service struct{ store Store }

// New returns the service.
func New(store Store) *Service { return &Service{store: store} }

// Report is a change a person or a pipeline reports. At defaults to now;
// TargetID nil means every instance.
type Report struct {
	TargetID *int64         `json:"target_id"`
	At       *time.Time     `json:"at"`
	Kind     string         `json:"kind"`
	Title    string         `json:"title"`
	Details  map[string]any `json:"details"`
}

// Validate returns field errors.
func (r Report) Validate(now time.Time) map[string]string {
	errs := map[string]string{}
	if !slices.Contains(ReportedKinds, r.Kind) {
		errs["kind"] = "one of " + strings.Join(ReportedKinds, ", ")
	}
	if t := strings.TrimSpace(r.Title); t == "" || len(t) > maxTitle {
		errs["title"] = "1 to 200 characters"
	}
	if r.At != nil && (r.At.Before(now.Add(-maxReportedIn)) || r.At.After(now.Add(5*time.Minute))) {
		errs["at"] = "within the last 30 days"
	}
	if raw, err := json.Marshal(r.Details); err != nil || len(raw) > maxDetails {
		errs["details"] = "a JSON object of at most 16 KiB"
	}
	return errs
}

// Record stores a reported change (already validated) from a source
// (SourceAPI or SourceUser) by a person or token name.
func (s *Service) Record(ctx context.Context, r Report, source, by string, now time.Time) (Event, error) {
	at := now
	if r.At != nil {
		at = *r.At
	}
	e := Event{TargetID: r.TargetID, At: at.UTC(), Kind: r.Kind, Source: source, Title: strings.TrimSpace(r.Title),
		Details: r.Details, CreatedBy: by}
	if e.Details == nil {
		e.Details = map[string]any{}
	}
	events := []Event{e}
	if err := s.store.InsertChanges(ctx, events); err != nil {
		return Event{}, err
	}
	return events[0], nil
}

// List returns changes, the newest first.
func (s *Service) List(ctx context.Context, f repository.ChangeFilter) ([]Event, error) {
	return s.store.ListChanges(ctx, f)
}

// Delete deletes a reported change; detected changes cannot be deleted.
func (s *Service) Delete(ctx context.Context, id int64) (Event, error) {
	return s.store.DeleteChange(ctx, id)
}
