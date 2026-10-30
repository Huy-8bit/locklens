package insight

import (
	"fmt"
	"math"

	"github.com/locklens/locklens/internal/domain/logs"
	"github.com/locklens/locklens/internal/domain/observe"
)

// weighLogs adds the evidence of the server log: lock waits and temporary
// files of the statement itself are direct evidence; lock waits and
// deadlocks of other statements are evidence of contention on the
// instance.
func weighLogs(sig Signals, lock, plan *hyp) {
	var ownWaits, otherWaits, deadlocks, ownTemp int
	var ownWaitMS float64
	var largestTemp int64
	own := func(e observe.LogEvent) bool {
		return sig.QueryID != nil && e.QueryID != nil && *e.QueryID == *sig.QueryID
	}
	for _, e := range sig.Logs {
		switch e.Kind {
		case logs.KindLockWait:
			if own(e) {
				ownWaits++
				if e.DurationMS != nil {
					ownWaitMS = math.Max(ownWaitMS, *e.DurationMS)
				}
			} else {
				otherWaits++
			}
		case logs.KindDeadlock:
			deadlocks++
		case logs.KindTempFile:
			if own(e) {
				ownTemp++
				if b, ok := e.Details["bytes"].(float64); ok && int64(b) > largestTemp {
					largestTemp = int64(b)
				}
			}
		}
	}
	if ownWaits > 0 {
		lock.support(math.Min(0.5, 0.2+0.05*float64(ownWaits)),
			"The server log shows this statement waiting for a lock %d time(s), the longest %.0f ms (log_lock_waits).", ownWaits, ownWaitMS)
	}
	if otherWaits+deadlocks > 0 {
		lock.support(math.Min(0.25, 0.03*float64(otherWaits+3*deadlocks)),
			"The server log shows %d lock wait(s) and %d deadlock(s) of other statements in the period.", otherWaits, deadlocks)
	}
	if ownTemp > 0 {
		plan.support(0.15, "The server log shows %d temporary file(s) written by this statement%s.", ownTemp, largest(largestTemp))
	}
}

func largest(b int64) string {
	if b <= 0 {
		return ""
	}
	return fmt.Sprintf(" (the largest %.1f MB)", float64(b)/(1<<20))
}
