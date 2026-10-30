package queries

import _ "embed"

//go:embed cluster/heartbeat.sql
var Heartbeat string

//go:embed cluster/live_servers.sql
var LiveServers string

//go:embed cluster/hold_lease.sql
var HoldLease string

//go:embed cluster/release_lease.sql
var ReleaseLease string

//go:embed cluster/list_leases.sql
var ListLeases string

//go:embed cluster/save_live_state.sql
var SaveLiveState string

//go:embed cluster/live_state.sql
var LiveState string

//go:embed cluster/notify.sql
var Notify string

// Listen subscribes the session to a notification channel.
func Listen(channel string) string { return "LISTEN " + quoteIdent(channel) }

// UnlistenAll ends every subscription of the session.
const UnlistenAll = "UNLISTEN *"
