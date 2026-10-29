//go:build !cgo

package sqlparse

const available = false

// Analyze returns ErrUnavailable: this build has no parser.
func Analyze(string) (Analysis, error) { return Analysis{}, ErrUnavailable }
