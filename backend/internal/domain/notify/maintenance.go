package notify

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
	// Weekly windows are in IANA time zones; the database is embedded so
	// they work in images without one (distroless, scratch).
	_ "time/tzdata"

	"github.com/locklens/locklens/internal/domain/changes"
	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/domain/rules"
	"github.com/locklens/locklens/internal/repository"
)

// maxAfterDeploy bounds an after-deploy window, so finding the deploys that
// matter reads a bounded period.
const maxAfterDeploy = 24 * 60

// WindowInput creates or changes a maintenance window.
type WindowInput struct {
	Name            string     `json:"name"`
	Enabled         bool       `json:"enabled"`
	Kind            string     `json:"kind"`
	Environments    []string   `json:"environments"`
	TargetIDs       []int64    `json:"target_ids"`
	RuleIDs         []string   `json:"rule_ids"`
	StartsAt        *time.Time `json:"starts_at"`
	EndsAt          *time.Time `json:"ends_at"`
	Weekdays        []int      `json:"weekdays"`
	StartTime       string     `json:"start_time"`
	DurationMinutes int        `json:"duration_minutes"`
	Timezone        string     `json:"timezone"`
}

// validate returns field errors.
func (in *WindowInput) validate() map[string]string {
	f := map[string]string{}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len([]rune(in.Name)) > 100 {
		f["name"] = "between 1 and 100 characters"
	}
	for _, e := range in.Environments {
		if !envPattern.MatchString(e) {
			f["environments"] = "lower-case letters, digits, '-' and '_'"
		}
	}
	for _, r := range in.RuleIDs {
		if !slices.Contains(rules.AllRules, r) {
			f["rule_ids"] = "unknown rule " + r
		}
	}
	if in.Timezone == "" {
		in.Timezone = "UTC"
	}
	switch in.Kind {
	case repository.MaintenanceOnce:
		if in.StartsAt == nil || in.EndsAt == nil || !in.EndsAt.After(*in.StartsAt) {
			f["ends_at"] = "a start and a later end"
		}
	case repository.MaintenanceWeekly:
		if len(in.Weekdays) == 0 {
			f["weekdays"] = "at least one day"
		}
		for _, d := range in.Weekdays {
			if d < 0 || d > 6 {
				f["weekdays"] = "0 (Sunday) to 6 (Saturday)"
			}
		}
		if _, _, ok := clock(in.StartTime); !ok {
			f["start_time"] = "HH:MM"
		}
		if _, err := time.LoadLocation(in.Timezone); err != nil {
			f["timezone"] = "an IANA time zone such as Europe/Berlin"
		}
		if in.DurationMinutes < 1 || in.DurationMinutes > 7*24*60 {
			f["duration_minutes"] = "between 1 minute and 7 days"
		}
	case repository.MaintenanceAfterDeploy:
		if in.DurationMinutes < 1 || in.DurationMinutes > maxAfterDeploy {
			f["duration_minutes"] = "between 1 minute and 1 day"
		}
	default:
		f["kind"] = "once, weekly, or after_deploy"
	}
	return f
}

// clock parses HH:MM.
func clock(s string) (int, int, bool) {
	h, m, ok := strings.Cut(s, ":")
	hh, err1 := strconv.Atoi(h)
	mm, err2 := strconv.Atoi(m)
	return hh, mm, ok && err1 == nil && err2 == nil && hh >= 0 && hh < 24 && mm >= 0 && mm < 60 && len(m) == 2
}

// covers reports whether a window applies to an incident.
func covers(w repository.MaintenanceWindow, in repository.Incident, env string) bool {
	if !w.Enabled {
		return false
	}
	if len(w.Environments) > 0 && !slices.Contains(w.Environments, env) {
		return false
	}
	if len(w.TargetIDs) > 0 && (in.TargetID == nil || !slices.Contains(w.TargetIDs, *in.TargetID)) {
		return false
	}
	return len(w.RuleIDs) == 0 || slices.Contains(w.RuleIDs, in.RuleID)
}

// activeAt reports whether a window is in effect at now; deploys are the
// reported deploys and migrations of the incident's instance.
func activeAt(w repository.MaintenanceWindow, now time.Time, deploys []observe.ChangeEvent) bool {
	switch w.Kind {
	case repository.MaintenanceOnce:
		return w.StartsAt != nil && w.EndsAt != nil && !now.Before(*w.StartsAt) && now.Before(*w.EndsAt)
	case repository.MaintenanceWeekly:
		loc, err := time.LoadLocation(w.Timezone)
		if err != nil {
			loc = time.UTC
		}
		h, m, ok := clock(w.StartTime)
		if !ok {
			return false
		}
		dur := time.Duration(w.DurationMinutes) * time.Minute
		local := now.In(loc)
		// A window can start on an earlier day and still be running.
		for back := 0; back <= 7; back++ {
			day := local.AddDate(0, 0, -back)
			start := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
			if slices.Contains(w.Weekdays, int(start.Weekday())) && !now.Before(start) && now.Before(start.Add(dur)) {
				return true
			}
		}
		return false
	case repository.MaintenanceAfterDeploy:
		for _, d := range deploys {
			if !now.Before(d.At) && now.Before(d.At.Add(time.Duration(w.DurationMinutes)*time.Minute)) {
				return true
			}
		}
	}
	return false
}

// inMaintenance returns the maintenance window an incident is in now, if
// any. ws are the stored windows.
func (s *service) inMaintenance(ctx context.Context, ws []repository.MaintenanceWindow, in repository.Incident, env string, now time.Time) (*repository.MaintenanceWindow, error) {
	var deploys []observe.ChangeEvent
	loaded := false
	for i, w := range ws {
		if !covers(w, in, env) {
			continue
		}
		if w.Kind == repository.MaintenanceAfterDeploy && !loaded && in.TargetID != nil {
			loaded = true
			var err error
			deploys, err = s.store.ListChanges(ctx, repository.ChangeFilter{From: now.Add(-maxAfterDeploy * time.Minute), To: now.Add(time.Second),
				TargetIDs: []int64{*in.TargetID}, Kinds: []string{changes.KindDeploy, changes.KindMigration}, Limit: 100})
			if err != nil {
				return nil, err
			}
		}
		if activeAt(w, now, deploys) {
			return &ws[i], nil
		}
	}
	return nil, nil
}

// MaintenanceWindows returns every maintenance window.
func (s *service) MaintenanceWindows(ctx context.Context) ([]repository.MaintenanceWindow, error) {
	return s.store.MaintenanceWindows(ctx)
}

// SaveMaintenanceWindow creates (id 0) or replaces a maintenance window.
func (s *service) SaveMaintenanceWindow(ctx context.Context, id int64, in WindowInput, by string) (repository.MaintenanceWindow, error) {
	if f := in.validate(); len(f) > 0 {
		return repository.MaintenanceWindow{}, &ValidationError{Fields: f}
	}
	w := repository.MaintenanceWindow{ID: id, Name: in.Name, Enabled: in.Enabled, Kind: in.Kind, Environments: in.Environments,
		TargetIDs: in.TargetIDs, RuleIDs: in.RuleIDs, Weekdays: in.Weekdays, StartTime: in.StartTime, DurationMinutes: in.DurationMinutes,
		Timezone: in.Timezone, CreatedBy: by}
	if in.Kind == repository.MaintenanceOnce {
		w.StartsAt, w.EndsAt = in.StartsAt, in.EndsAt
	}
	out, err := s.store.SaveMaintenanceWindow(ctx, w)
	if errors.Is(err, repository.ErrNotFound) {
		return out, fmt.Errorf("maintenance window %d: %w", id, err)
	}
	return out, err
}

// DeleteMaintenanceWindow deletes a maintenance window.
func (s *service) DeleteMaintenanceWindow(ctx context.Context, id int64) error {
	return s.store.DeleteMaintenanceWindow(ctx, id)
}
