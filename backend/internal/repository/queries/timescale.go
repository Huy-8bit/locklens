package queries

import _ "embed"

//go:embed timescale/timescale_available.sql
var TimescaleAvailable string

//go:embed timescale/create_timescale_extension.sql
var CreateTimescaleExtension string

//go:embed timescale/timescale_version.sql
var TimescaleVersion string

//go:embed timescale/existing_hypertables.sql
var ExistingHypertables string

//go:embed timescale/drop_activity_snapshots_primary_key.sql
var DropActivitySnapshotsPrimaryKey string

//go:embed timescale/create_hypertable.sql
var CreateHypertable string

//go:embed timescale/compression_enabled.sql
var CompressionEnabled string

//go:embed timescale/remove_compression_policy.sql
var RemoveCompressionPolicy string

//go:embed timescale/add_compression_policy.sql
var AddCompressionPolicy string
