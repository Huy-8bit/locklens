package observe

import "time"

// Catalog is the schema of the database LockLens connects to, as the
// query optimizer needs it: indexes with their definitions, table sizes and
// access counters, foreign keys, and column statistics. It holds no data
// values: column statistics are limited to null_frac, n_distinct, and
// correlation (never most_common_vals or histograms).
type Catalog struct {
	At          time.Time         `json:"at"`
	DatName     string            `json:"datname"`
	StatsReset  *time.Time        `json:"stats_reset"`
	Tables      []CatalogTable    `json:"tables"`
	Indexes     []CatalogIndex    `json:"indexes"`
	ForeignKeys []CatalogFK       `json:"foreign_keys"`
	Columns     []ColumnStats     `json:"columns"`
	Warnings    []string          `json:"warnings"`
	Extensions  map[string]string `json:"extensions"`
	// Settings are planner settings in their base units (work_mem in kB).
	Settings map[string]string `json:"settings"`
	// VersionNum is server_version_num.
	VersionNum int `json:"server_version_num"`
	// InRecovery is true on a standby: the schema is its primary's, and
	// changes are made there.
	InRecovery bool `json:"in_recovery"`
	// Truncated is true when tables or indexes were cut at their limit, so
	// the lists are not the whole schema.
	Truncated bool `json:"truncated,omitempty"`
}

// CatalogTable is one table with its size estimates and access counters.
type CatalogTable struct {
	RelID            uint32     `json:"relid"`
	Schema           string     `json:"schema"`
	Name             string     `json:"name"`
	RelTuples        float64    `json:"reltuples"`
	RelPages         int64      `json:"relpages"`
	SeqScan          int64      `json:"seq_scan"`
	SeqTupRead       int64      `json:"seq_tup_read"`
	IdxScan          int64      `json:"idx_scan"`
	Writes           int64      `json:"writes"`
	LiveTuples       int64      `json:"n_live_tup"`
	ModSinceAnalyze  int64      `json:"n_mod_since_analyze"`
	LastAnalyze      *time.Time `json:"last_analyze"`
	LastAutoAnalyze  *time.Time `json:"last_autoanalyze"`
	PartitionedChild bool       `json:"partition"`
}

// Qualified is schema.name.
func (t CatalogTable) Qualified() string { return t.Schema + "." + t.Name }

// CatalogIndex is one index.
type CatalogIndex struct {
	RelID      uint32 `json:"indexrelid"`
	TableRelID uint32 `json:"relid"`
	Schema     string `json:"schema"`
	Table      string `json:"table"`
	Name       string `json:"name"`
	Method     string `json:"method"`
	// Columns are the key columns in order; an expression is "" (see
	// Definition).
	Columns     []string `json:"columns"`
	KeyColumns  int      `json:"key_columns"`
	Unique      bool     `json:"unique"`
	Primary     bool     `json:"primary"`
	Valid       bool     `json:"valid"`
	Partial     bool     `json:"partial"`
	Expressions bool     `json:"expressions"`
	Constraint  bool     `json:"constraint"`
	Definition  string   `json:"definition"`
	SizeBytes   int64    `json:"size_bytes"`
	Scans       *int64   `json:"idx_scan"`
}

// Qualified is schema.name of the index.
func (i CatalogIndex) Qualified() string { return i.Schema + "." + i.Name }

// TableQualified is schema.table of the indexed table.
func (i CatalogIndex) TableQualified() string { return i.Schema + "." + i.Table }

// CatalogFK is one foreign key of a referencing table.
type CatalogFK struct {
	Name       string   `json:"name"`
	Schema     string   `json:"schema"`
	Table      string   `json:"table"`
	Columns    []string `json:"columns"`
	RefSchema  string   `json:"ref_schema"`
	RefTable   string   `json:"ref_table"`
	OnDelete   string   `json:"on_delete"`
	TableRelID uint32   `json:"relid"`
}

// ColumnStats are planner statistics of one column without data values.
type ColumnStats struct {
	Schema      string   `json:"schema"`
	Table       string   `json:"table"`
	Column      string   `json:"column"`
	NullFrac    float64  `json:"null_frac"`
	NDistinct   float64  `json:"n_distinct"`
	Correlation *float64 `json:"correlation"`
}
