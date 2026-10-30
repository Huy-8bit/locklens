// Package tokens issues and checks API tokens for machines: deploy
// pipelines that report changes and log agents that ship log events. A
// token is random, shown once, and stored only as a SHA-256 hash; it may be
// limited to one instance.
package tokens

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"strings"

	"github.com/locklens/locklens/internal/repository"
)

// prefix starts every token so it can be recognised (by secret scanners).
const prefix = "llk_"

// Errors of Authenticate.
var (
	ErrInvalid = errors.New("invalid or revoked API token")
	ErrScope   = errors.New("the API token does not allow this")
)

// Service issues and checks tokens.
type Service struct{ store Store }

// New returns the service.
func New(store Store) *Service { return &Service{store: store} }

// Input is a token to create.
type Input struct {
	Name     string `json:"name"`
	Scope    string `json:"scope"`
	TargetID *int64 `json:"target_id"`
}

// Validate returns field errors.
func (in Input) Validate() map[string]string {
	errs := map[string]string{}
	if n := strings.TrimSpace(in.Name); n == "" || len(n) > 100 {
		errs["name"] = "1 to 100 characters"
	}
	if !slices.Contains([]string{repository.TokenEvents, repository.TokenIngest}, in.Scope) {
		errs["scope"] = "events or ingest"
	}
	if in.Scope == repository.TokenIngest && in.TargetID == nil {
		errs["target_id"] = "a log agent token belongs to one instance"
	}
	return errs
}

// Create issues a token; the secret is returned only here.
func (s *Service) Create(ctx context.Context, in Input, by string) (repository.APIToken, string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return repository.APIToken{}, "", err
	}
	secret := prefix + base64.RawURLEncoding.EncodeToString(b)
	t, err := s.store.CreateToken(ctx, strings.TrimSpace(in.Name), in.Scope, in.TargetID, hash(secret), secret[:len(prefix)+4], by)
	return t, secret, err
}

// Authenticate returns the live token of a secret if it has the scope and,
// when it is limited to an instance, targetID is that instance (0 accepts
// any instance the token allows; the caller checks it then).
func (s *Service) Authenticate(ctx context.Context, secret, scope string, targetID int64) (repository.APIToken, error) {
	if !strings.HasPrefix(secret, prefix) {
		return repository.APIToken{}, ErrInvalid
	}
	t, err := s.store.TokenByHash(ctx, hash(secret))
	if errors.Is(err, repository.ErrNotFound) {
		return t, ErrInvalid
	}
	if err != nil {
		return t, err
	}
	if t.Scope != scope || targetID != 0 && t.TargetID != nil && *t.TargetID != targetID {
		return t, ErrScope
	}
	return t, nil
}

// List returns every token.
func (s *Service) List(ctx context.Context) ([]repository.APIToken, error) {
	return s.store.ListTokens(ctx)
}

// Revoke revokes a token.
func (s *Service) Revoke(ctx context.Context, id int64) error { return s.store.RevokeToken(ctx, id) }

func hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
