package workspace

import (
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"testing"
)

func TestSafeErrorNeverIncludesDatabaseValues(t *testing.T) {
	e := SafeError(&pgconn.PgError{Code: "22000", Message: "secret-value", Detail: "password", Position: 60})
	if e.Message == "secret-value" || e.Code != "22000" || e.Position != 60-int32(len(declaration)) {
		t.Fatal(e)
	}
	if SafeError(errors.New("dsn-with-password")).Message == "dsn-with-password" {
		t.Fatal("raw error leaked")
	}
}
func TestCancelRequiresSessionAndTarget(t *testing.T) {
	called := false
	s := New(nil, nil)
	s.active["request"] = running{"owner", 3, func() { called = true }}
	if s.Cancel("other", 3, "request") || s.Cancel("owner", 4, "request") || called {
		t.Fatal("cancel escaped ownership")
	}
	if !s.Cancel("owner", 3, "request") || !called {
		t.Fatal("owner cannot cancel")
	}
}
