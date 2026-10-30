package tokens

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/locklens/locklens/internal/repository"
)

// memStore keeps tokens in memory.
type memStore struct {
	hashes [][]byte
	tokens []repository.APIToken
}

func (m *memStore) CreateToken(_ context.Context, name, scope string, targetID *int64, hash []byte, prefix, by string) (repository.APIToken, error) {
	t := repository.APIToken{ID: int64(len(m.tokens) + 1), Name: name, Scope: scope, TargetID: targetID, Prefix: prefix, CreatedBy: by}
	m.hashes, m.tokens = append(m.hashes, hash), append(m.tokens, t)
	return t, nil
}
func (m *memStore) ListTokens(context.Context) ([]repository.APIToken, error) { return m.tokens, nil }
func (m *memStore) TokenByHash(_ context.Context, hash []byte) (repository.APIToken, error) {
	for i, h := range m.hashes {
		if bytes.Equal(h, hash) && m.tokens[i].RevokedAt == nil {
			return m.tokens[i], nil
		}
	}
	return repository.APIToken{}, repository.ErrNotFound
}
func (m *memStore) RevokeToken(context.Context, int64) error { return nil }

func TestTokens(t *testing.T) {
	ctx := context.Background()
	st := &memStore{}
	s := New(st)
	seven := int64(7)
	tok, secret, err := s.Create(ctx, Input{Name: "agent", Scope: repository.TokenIngest, TargetID: &seven}, "admin")
	if err != nil || !strings.HasPrefix(secret, "llk_") || len(secret) < 30 || !strings.HasPrefix(secret, tok.Prefix) {
		t.Fatalf("create: %+v %q %v", tok, secret, err)
	}
	if bytes.Contains(st.hashes[0], []byte(secret)) {
		t.Fatal("only a hash is stored")
	}
	if _, err := s.Authenticate(ctx, secret, repository.TokenIngest, 7); err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if _, err := s.Authenticate(ctx, secret, repository.TokenIngest, 8); !errors.Is(err, ErrScope) {
		t.Fatalf("another instance: %v", err)
	}
	if _, err := s.Authenticate(ctx, secret, repository.TokenEvents, 7); !errors.Is(err, ErrScope) {
		t.Fatalf("another scope: %v", err)
	}
	if _, err := s.Authenticate(ctx, "llk_wrong", repository.TokenIngest, 7); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong secret: %v", err)
	}
	if errs := (Input{Name: "x", Scope: repository.TokenIngest}).Validate(); errs["target_id"] == "" {
		t.Fatalf("an ingest token needs an instance: %v", errs)
	}
}
