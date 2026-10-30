package queries

import _ "embed"

//go:embed replication/replicas.sql
var Replicas string

//go:embed replication/slots.sql
var Slots string

//go:embed replication/slots_inactive_since.sql
var SlotsInactiveSince string

//go:embed replication/standby.sql
var Standby string

//go:embed replication/wal_receiver.sql
var WalReceiver string

//go:embed replication/archiver.sql
var Archiver string

//go:embed replication/current_wal_lsn.sql
var CurrentWALLSN string
