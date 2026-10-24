package config

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
)

// Redacted replaces secrets in redacted output.
const Redacted = "[REDACTED]"

// Redactor removes known secret values from text.
//
// It works on exact values taken from the parsed configuration (the raw DSN,
// the password, and their URL-encoded forms) rather than on patterns, so it
// also catches secrets that do not look like "password=..." — for example a
// password echoed back inside a driver error. Values too short to be
// meaningful are ignored to avoid mangling unrelated text.
type Redactor struct {
	secrets []string
}

// NewRedactor returns a Redactor that knows the raw DSN and any password that
// can be extracted from it without the driver's help.
func NewRedactor(dsn string) *Redactor {
	r := &Redactor{}
	r.Add(dsn)
	if u, err := url.Parse(dsn); err == nil && u.User != nil {
		if pw, ok := u.User.Password(); ok {
			r.Add(pw)
		}
	}
	return r
}

// Add registers a secret value together with its URL-encoded variants.
func (r *Redactor) Add(secret string) {
	if r == nil || len(secret) < 3 {
		return
	}
	for _, v := range []string{secret, url.QueryEscape(secret), url.PathEscape(secret), url.UserPassword("", secret).String()[1:]} {
		if len(v) >= 3 && !slices.Contains(r.secrets, v) {
			r.secrets = append(r.secrets, v)
		}
	}
}

// String returns s with every registered secret replaced.
func (r *Redactor) String(s string) string {
	if r == nil {
		return s
	}
	// Longest first so a DSN containing the password is replaced as a whole.
	for _, secret := range sortedByLength(r.secrets) {
		s = strings.ReplaceAll(s, secret, Redacted)
	}
	return s
}

// Error wraps err so its message is redacted. errors.Is and errors.As still
// see the original chain.
func (r *Redactor) Error(err error) error {
	if err == nil {
		return nil
	}
	return &redactedError{err: err, msg: r.String(err.Error())}
}

type redactedError struct {
	err error
	msg string
}

func (e *redactedError) Error() string { return e.msg }
func (e *redactedError) Unwrap() error { return e.err }

func sortedByLength(list []string) []string {
	out := slices.Clone(list)
	slices.SortStableFunc(out, func(a, b string) int { return cmp.Compare(len(b), len(a)) })
	return out
}
