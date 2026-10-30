package targetdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/targetdb/queries"
)

// ReadServerState reads the server facts and every setting. Settings that
// can hold secrets keep only a fingerprint of their value.
func ReadServerState(ctx context.Context, tx pgx.Tx) (observe.ServerState, error) {
	st := observe.ServerState{At: time.Now().UTC(), Settings: map[string]observe.Setting{}}
	f := &st.Facts
	if err := tx.QueryRow(ctx, queries.ProcessFacts).Scan(&f.StartedAt, &f.ConfLoadedAt, &f.InRecovery, &f.VersionNum, &f.Version); err != nil {
		return st, err
	}
	f.StartedAt, f.ConfLoadedAt = f.StartedAt.UTC(), f.ConfLoadedAt.UTC()
	rows, err := tx.Query(ctx, queries.Settings)
	if err != nil {
		return st, err
	}
	var name string
	var s observe.Setting
	_, err = pgx.ForEachRow(rows, []any{&name, &s.Value, &s.Unit, &s.Source, &s.PendingRestart}, func() error {
		if SecretSetting(name) && s.Value != "" {
			s.Value = Fingerprint(s.Value)
		}
		st.Settings[name] = s
		return nil
	})
	return st, err
}

// SecretSetting says whether a setting can hold secrets: connection strings
// (with passwords) and shell commands (with keys or tokens).
func SecretSetting(name string) bool {
	for _, part := range []string{"conninfo", "command", "password", "passphrase", "secret", "key_file"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

// Fingerprint stands in for a secret value: equal values have equal
// fingerprints, and the value cannot be recovered.
func Fingerprint(v string) string {
	sum := sha256.Sum256([]byte(v))
	return "sha256:" + hex.EncodeToString(sum[:6])
}
