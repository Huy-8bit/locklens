// Package queries holds every SQL statement the monitoring store runs, one
// statement per .sql file, grouped in a directory per area (targets,
// incidents, series, …). The Go files only embed them: to change what the
// store reads or writes, edit the .sql file named after the method of
// repository.Store that runs it.
//
// A few statements depend on a table, a column list, or an expression that
// LockLens chooses from fixed lists in this package (never from a request).
// Their .sql files contain {{placeholder}} markers, filled by the builder
// functions in builders.go.
package queries
