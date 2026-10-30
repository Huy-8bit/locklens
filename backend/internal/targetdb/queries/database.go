package queries

import _ "embed"

//go:embed database/database_counters.sql
var DatabaseCounters string

//go:embed database/server_start.sql
var ServerStart string

//go:embed database/bgwriter16.sql
var Bgwriter16 string

//go:embed database/checkpointer17.sql
var Checkpointer17 string

//go:embed database/bgwriter17.sql
var Bgwriter17 string

//go:embed database/stat_i_o.sql
var StatIO string

//go:embed database/w_a_l_position.sql
var WALPosition string

//go:embed database/archiver_counters.sql
var ArchiverCounters string
