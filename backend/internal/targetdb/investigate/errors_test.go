package investigate

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
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
