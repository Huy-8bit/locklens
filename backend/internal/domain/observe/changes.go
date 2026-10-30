package observe

import "time"

// Setting is one server setting (pg_settings) as change detection compares
// it. Value is the setting in its base unit; for settings that can hold
// secrets (connection strings, shell commands) it is a fingerprint, never
// the value.
type Setting struct {
	Value          string `json:"value"`
	Unit           string `json:"unit,omitempty"`
	Source         string `json:"source"`
	PendingRestart bool   `json:"pending_restart,omitempty"`
}

// ServerFacts are the facts about the server process that reveal restarts,
// promotions, and upgrades.
type ServerFacts struct {
	StartedAt    time.Time `json:"started_at"`
	ConfLoadedAt time.Time `json:"conf_loaded_at"`
	InRecovery   bool      `json:"in_recovery"`
	VersionNum   int       `json:"server_version_num"`
	Version      string    `json:"server_version"`
}

// ServerState is one reading of the settings and the server facts.
type ServerState struct {
	At       time.Time          `json:"at"`
	Facts    ServerFacts        `json:"facts"`
	Settings map[string]Setting `json:"settings"`
}

// ChangeEvent is one change on an instance (see package changes). TargetID
// nil means it concerns every instance (an application deploy that touches
// several databases).
type ChangeEvent struct {
	ID        int64          `json:"id"`
	TargetID  *int64         `json:"target_id"`
	At        time.Time      `json:"at"`
	Kind      string         `json:"kind"`
	Source    string         `json:"source"`
	Title     string         `json:"title"`
	Details   map[string]any `json:"details"`
	CreatedBy string         `json:"created_by"`
}
