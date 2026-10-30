package workspace

import "github.com/locklens/locklens/internal/targetdb/investigate"

// Error is an error with a code and a message safe to show to the user.
type Error = investigate.Error

// SafeError turns any error into one that is safe to show (no values from
// the database).
func SafeError(err error) *Error { return investigate.SafeError(err) }
