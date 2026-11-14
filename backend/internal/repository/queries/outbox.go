package queries

import _ "embed"

//go:embed outbox/enqueue.sql
var EnqueueNotification string

//go:embed outbox/claim.sql
var ClaimNotifications string

//go:embed outbox/done.sql
var DoneNotifications string

//go:embed outbox/pending.sql
var PendingNotifications string
