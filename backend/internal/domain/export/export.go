// Package export produces offline incident exports: a versioned JSON
// document and a standalone HTML report that opens without a backend.
//
// Exports never contain connection strings or credentials (incidents do not
// store them). Query text is removed unless explicitly requested, and even
// then only exists where the target had opted in to query-text collection.
package export

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
	"unicode/utf8"

	"github.com/locklens/locklens/internal/domain/model"
	"github.com/locklens/locklens/internal/repository"
)

// Format identifies LockLens incident exports.
const Format = "locklens-incident"

// SchemaVersion is the export schema version written by this build.
const SchemaVersion = 1

// MaxImportBytes bounds imported files.
const MaxImportBytes = 10 << 20

// Incident is the export document.
type Incident struct {
	Format            string                     `json:"format"`
	SchemaVersion     int                        `json:"schema_version"`
	ExportedAt        time.Time                  `json:"exported_at"`
	Tool              model.Tool                 `json:"tool"`
	QueryTextIncluded bool                       `json:"query_text_included"`
	Incident          repository.Incident        `json:"incident"`
	Findings          []repository.FindingRow    `json:"findings"`
	Events            []repository.IncidentEvent `json:"events"`
}

// Repositories are the stores an export reads.
type Repositories struct {
	repository.IncidentRepository
	repository.FindingRepository
}

// Build assembles an export from the store.
func Build(ctx context.Context, src Repositories, id int64, includeQuery bool, tool model.Tool) (Incident, error) {
	in, err := src.GetIncident(ctx, id)
	if err != nil {
		return Incident{}, err
	}
	var events []repository.IncidentEvent
	var after int64
	for {
		page, err := src.ListIncidentEvents(ctx, id, after, 2000)
		if err != nil {
			return Incident{}, err
		}
		events = append(events, page...)
		if len(page) < 2000 {
			break
		}
		after = page[len(page)-1].ID
	}
	findings, err := src.ListFindings(ctx, repository.FindingFilter{IncidentID: id, Limit: 500})
	if err != nil {
		return Incident{}, err
	}
	out := Incident{Format: Format, SchemaVersion: SchemaVersion, ExportedAt: time.Now().UTC(), Tool: tool,
		QueryTextIncluded: includeQuery, Incident: in, Findings: findings, Events: events}
	if !includeQuery {
		out.Events = scrubEvents(out.Events)
	}
	if out.Events == nil {
		out.Events = []repository.IncidentEvent{}
	}
	if out.Findings == nil {
		out.Findings = []repository.FindingRow{}
	}
	return out, nil
}

// queryKeys are JSON keys that carry SQL text in event payloads: session
// query text in blocking snapshots and statement text in workload events.
var queryKeys = map[string]bool{"query": true, "query_truncated": true, "text": true}

func scrubEvents(evs []repository.IncidentEvent) []repository.IncidentEvent {
	out := make([]repository.IncidentEvent, len(evs))
	for i, e := range evs {
		out[i] = e
		var v any
		if json.Unmarshal(e.Payload, &v) != nil {
			out[i].Payload = json.RawMessage("{}")
			continue
		}
		scrub(v)
		raw, err := json.Marshal(v)
		if err != nil {
			raw = []byte("{}")
		}
		out[i].Payload = raw
	}
	return out
}

func scrub(v any) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			if queryKeys[k] {
				delete(x, k)
				continue
			}
			scrub(child)
		}
	case []any:
		for _, child := range x {
			scrub(child)
		}
	}
}

// WriteJSON writes an export as indented JSON.
func WriteJSON(w io.Writer, e Incident) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(true)
	return enc.Encode(e)
}

// Decode reads and validates an export (for imports and the offline report
// command). Size is bounded and the format and version are checked.
func Decode(r io.Reader) (Incident, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxImportBytes+1))
	if err != nil {
		return Incident{}, err
	}
	if len(data) > MaxImportBytes {
		return Incident{}, fmt.Errorf("incident file exceeds the %d MiB limit", MaxImportBytes>>20)
	}
	var probe struct {
		Format        string `json:"format"`
		SchemaVersion int    `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return Incident{}, fmt.Errorf("not valid JSON: %w", err)
	}
	if probe.Format != Format {
		return Incident{}, errors.New("not a LockLens incident export (format field is missing or different)")
	}
	if probe.SchemaVersion != SchemaVersion {
		return Incident{}, fmt.Errorf("unsupported incident export schema_version %d (this build reads %d)", probe.SchemaVersion, SchemaVersion)
	}
	var e Incident
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&e); err != nil {
		return Incident{}, fmt.Errorf("invalid incident export: %w", err)
	}
	return e, validate(e)
}

func validate(e Incident) error {
	str := func(name, s string, max int) error {
		if !utf8.ValidString(s) {
			return fmt.Errorf("%s is not valid UTF-8", name)
		}
		if utf8.RuneCountInString(s) > max {
			return fmt.Errorf("%s is longer than %d characters", name, max)
		}
		return nil
	}
	if e.Incident.Title == "" {
		return errors.New("incident title is missing")
	}
	if err := errors.Join(str("title", e.Incident.Title, 500), str("target_name", e.Incident.TargetName, 200),
		str("rule_id", e.Incident.RuleID, 100), str("severity", e.Incident.Severity, 20)); err != nil {
		return err
	}
	if len(e.Events) > 20000 {
		return errors.New("too many events")
	}
	for i, ev := range e.Events {
		if err := errors.Join(str(fmt.Sprintf("events[%d].kind", i), ev.Kind, 50), str(fmt.Sprintf("events[%d].summary", i), ev.Summary, 2000)); err != nil {
			return err
		}
		if len(ev.Payload) > 0 && !json.Valid(ev.Payload) {
			return fmt.Errorf("events[%d].payload is not valid JSON", i)
		}
	}
	return nil
}
