package changes

import (
	"fmt"
	"slices"
	"strings"

	"github.com/locklens/locklens/internal/domain/observe"
)

// maxListed bounds the changed settings or objects one event lists.
const maxListed = 50

// SettingChange is one changed setting.
type SettingChange struct {
	Name   string `json:"name"`
	Before string `json:"before"`
	After  string `json:"after"`
	Unit   string `json:"unit,omitempty"`
	Source string `json:"source"`
	// PendingRestart is true when the new value takes effect only after a
	// restart.
	PendingRestart bool `json:"pending_restart,omitempty"`
}

// DiffServer compares two readings of the server: a restart, promotion,
// demotion, upgrade, and changed settings each give one event.
func DiffServer(prev, cur observe.ServerState) []Event {
	var out []Event
	p, c := prev.Facts, cur.Facts
	base := func(kind, title string) Event {
		return Event{At: cur.At, Kind: kind, Source: SourceDetected, Title: title, Details: window(prev.At, cur.At)}
	}
	if !c.StartedAt.Equal(p.StartedAt) && !c.StartedAt.IsZero() {
		e := base(KindRestart, "Server restarted")
		e.At = c.StartedAt // known exactly from pg_postmaster_start_time()
		e.Details["started_at"] = c.StartedAt
		e.Details["previous_start"] = p.StartedAt
		out = append(out, e)
	}
	if p.InRecovery && !c.InRecovery {
		out = append(out, base(KindPromotion, "Standby promoted to primary"))
	}
	if !p.InRecovery && c.InRecovery {
		out = append(out, base(KindDemotion, "Primary is now a standby (failover or rebuild)"))
	}
	if p.VersionNum != 0 && c.VersionNum != p.VersionNum {
		e := base(KindUpgrade, fmt.Sprintf("Server version changed from %s to %s", p.Version, c.Version))
		e.Details["before"], e.Details["after"] = p.Version, c.Version
		out = append(out, e)
	}
	if ch := diffSettings(prev.Settings, cur.Settings); len(ch) > 0 {
		e := base(KindSettings, settingsTitle(ch))
		if !c.ConfLoadedAt.Equal(p.ConfLoadedAt) && c.ConfLoadedAt.After(prev.At) {
			e.At = c.ConfLoadedAt // pg_conf_load_time(): the reload that applied them
			e.Details["conf_loaded_at"] = c.ConfLoadedAt
		}
		e.Details["changes"] = ch
		out = append(out, e)
	}
	return out
}

// diffSettings lists the settings whose value changed, by name. A setting
// that appears or disappears (an extension loaded or removed) counts too.
func diffSettings(prev, cur map[string]observe.Setting) []SettingChange {
	var out []SettingChange
	for name, c := range cur {
		p, ok := prev[name]
		if ok && p.Value == c.Value && p.PendingRestart == c.PendingRestart {
			continue
		}
		out = append(out, SettingChange{Name: name, Before: p.Value, After: c.Value, Unit: c.Unit, Source: c.Source, PendingRestart: c.PendingRestart})
	}
	for name, p := range prev {
		if _, ok := cur[name]; !ok {
			out = append(out, SettingChange{Name: name, Before: p.Value, Unit: p.Unit, Source: p.Source})
		}
	}
	slices.SortFunc(out, func(a, b SettingChange) int { return strings.Compare(a.Name, b.Name) })
	if len(out) > maxListed {
		out = out[:maxListed]
	}
	return out
}

func settingsTitle(ch []SettingChange) string {
	if len(ch) == 1 {
		c := ch[0]
		return fmt.Sprintf("Setting %s changed: %s → %s", c.Name, show(c.Before, c.Unit), show(c.After, c.Unit))
	}
	names := make([]string, 0, 3)
	for _, c := range ch[:min(3, len(ch))] {
		names = append(names, c.Name)
	}
	more := ""
	if len(ch) > 3 {
		more = fmt.Sprintf(" and %d more", len(ch)-3)
	}
	return fmt.Sprintf("%d settings changed: %s%s", len(ch), strings.Join(names, ", "), more)
}

func show(v, unit string) string {
	switch {
	case v == "":
		return "(unset)"
	case unit != "" && !strings.HasPrefix(v, "sha256:"):
		return v + " " + unit
	}
	return v
}
