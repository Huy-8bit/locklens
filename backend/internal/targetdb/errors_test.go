package targetdb

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestHintAndFatalClassification(t *testing.T) {
	tests := []struct {
		err   error
		hint  string
		fatal bool
	}{
		{&pgconn.PgError{Code: "28P01"}, "authentication failed", true},
		{&pgconn.PgError{Code: "3D000"}, "database does not exist", true},
		{&pgconn.PgError{Code: "42501"}, "permission denied", true},
		{&pgconn.PgError{Code: "57014"}, "statement_timeout", false},
		{&pgconn.PgError{Code: "53300"}, "max_connections", false},
		{fmt.Errorf("collect: %w", context.DeadlineExceeded), "timed out", false},
		{&UnsupportedVersionError{Version: "13.4"}, "versions below 14", true},
		{fmt.Errorf("x509: certificate signed by unknown authority"), "TLS negotiation failed", false},
	}
	for _, tt := range tests {
		if got := Hint(tt.err); !strings.Contains(got, tt.hint) {
			t.Errorf("Hint(%v) = %q, want it to mention %q", tt.err, got, tt.hint)
		}
		if got := IsFatal(tt.err); got != tt.fatal {
			t.Errorf("IsFatal(%v) = %v, want %v", tt.err, got, tt.fatal)
		}
	}
	if Hint(nil) != "" {
		t.Error("Hint(nil) must be empty")
	}
}
