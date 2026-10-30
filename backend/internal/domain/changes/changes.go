// Package changes records what changed on a monitored instance and when:
// changes LockLens detects (settings, restarts, promotions, upgrades,
// schema) and changes people or pipelines report (deploys, migrations).
// Change events are drawn on charts and weighed as possible explanations of
// anomalies; a change near an anomaly is a lead, never a confirmed cause.
//
// Detection compares each reading with the previous one (kept in the
// store), so a change is known to have happened between two readings; the
// event's time is the later reading and its details carry the window.
package changes

import (
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
)

// Kinds of change events.
const (
	KindSettings  = "settings"  // server settings changed (a reload or a restart)
	KindRestart   = "restart"   // the server process started again
	KindPromotion = "promotion" // a standby became a primary
	KindDemotion  = "demotion"  // a primary became a standby (failover, rebuild)
	KindUpgrade   = "upgrade"   // the server version changed
	KindSchema    = "schema"    // tables or indexes were created, dropped, or became (in)valid
	KindDeploy    = "deploy"    // an application was deployed
	KindMigration = "migration" // a schema migration ran
	KindConfig    = "config"    // a configuration change made outside the database
	KindNote      = "note"      // anything else worth marking
)

// ReportedKinds are the kinds people and pipelines may report.
var ReportedKinds = []string{KindDeploy, KindMigration, KindConfig, KindNote}

// Sources of change events.
const (
	SourceDetected = "detected" // found by LockLens
	SourceAPI      = "api"      // posted with an API token
	SourceUser     = "user"     // entered by a person
)

// Event is one change (stored by the repository, hence defined with the
// other observations).
type Event = observe.ChangeEvent

// window is the detection window of a detected change.
func window(prev, cur time.Time) map[string]any {
	return map[string]any{"detected_after": prev.UTC(), "detected_by": cur.UTC()}
}
