// Package tokens issues and checks API tokens for machines: deploy
// pipelines that report changes, log agents that ship log events,
// Prometheus scraping metrics, and scripts reading the API. A token is
// random, shown once, and stored only as a SHA-256 hash; it may be limited
// to one instance.
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

// Service issues API tokens and checks them.
type Service interface {
	// Create issues a token; the secret is returned only here.
	Create(ctx context.Context, in Input, by string) (repository.APIToken, string, error)
	// Authenticate returns the live token of a secret if it has the scope and,
	// when it is limited to an instance, targetID is that instance (0 accepts
	// any instance the token allows; the caller checks it then).
	Authenticate(ctx context.Context, secret, scope string, targetID int64) (repository.APIToken, error)
	// Ensure stores a token whose secret is given (from the environment, for
	// automation) unless it is stored already; the secret must look like a
	// generated one.
	Ensure(ctx context.Context, name, scope, secret string) error
	// List returns every token.
	List(ctx context.Context) ([]repository.APIToken, error)
	// Revoke revokes a token.
	Revoke(ctx context.Context, id int64) error
}

var _ Service = (*service)(nil)

// service issues and checks tokens.
type service struct{ store Repositories }

// New returns the service.
func New(store Repositories) Service { return &service{store: store} }

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
	if !slices.Contains([]string{repository.TokenEvents, repository.TokenIngest, repository.TokenMetrics, repository.TokenRead}, in.Scope) {
		errs["scope"] = "events, ingest, metrics, or read"
	}
	if in.Scope == repository.TokenIngest && in.TargetID == nil {
		errs["target_id"] = "a log agent token belongs to one instance"
	}
	if in.Scope == repository.TokenRead && in.TargetID != nil {
		errs["target_id"] = "a read token reads every instance a viewer may read"
	}
	return errs
}

// Create issues a token; the secret is returned only here.
func (s *service) Create(ctx context.Context, in Input, by string) (repository.APIToken, string, error) {
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
func (s *service) Authenticate(ctx context.Context, secret, scope string, targetID int64) (repository.APIToken, error) {
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

// Ensure stores a token whose secret is given (from the environment, for
// automation) unless it is stored already; the secret must look like a
// generated one.
func (s *service) Ensure(ctx context.Context, name, scope, secret string) error {
	if !strings.HasPrefix(secret, prefix) || len(secret) < len(prefix)+24 {
		return errors.New("the token must start with llk_ and have at least 24 more characters")
	}
	_, err := s.store.TokenByHash(ctx, hash(secret))
	if err == nil || !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	_, err = s.store.CreateToken(ctx, name, scope, nil, hash(secret), secret[:len(prefix)+4], "environment")
	return err
}

// List returns every token.
func (s *service) List(ctx context.Context) ([]repository.APIToken, error) {
	return s.store.ListTokens(ctx)
}

// Revoke revokes a token.
func (s *service) Revoke(ctx context.Context, id int64) error { return s.store.RevokeToken(ctx, id) }

func hash(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}
