// Package auth implements LockLens' authentication and role-based access.
//
// Passwords are hashed with bcrypt. Logged-in browsers hold an opaque random
// token in an HttpOnly, SameSite=Strict cookie; only its SHA-256 hash is
// stored server-side, so a store leak does not reveal usable tokens. Every
// state-changing request must carry the session's CSRF token in the
// X-CSRF-Token header and, if present, a same-origin Origin header.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/locklens/locklens/internal/repository"
)

// CookieName is the session cookie.
const CookieName = "locklens_session"

// CSRFHeader carries the CSRF token on mutations.
const CSRFHeader = "X-CSRF-Token"

// BcryptCost is the work factor for new hashes.
const BcryptCost = 12

// ErrInvalidCredentials is returned for a wrong username or password. It does
// not say which one was wrong.
var ErrInvalidCredentials = errors.New("invalid username or password")

// ErrRateLimited is returned when too many logins failed recently.
var ErrRateLimited = errors.New("too many failed login attempts; try again later")

// ValidatePassword checks the password length rules.
func ValidatePassword(password string) error {
	if len(password) < 12 {
		return errors.New("the password must be at least 12 characters long")
	}
	if len(password) > 72 {
		return errors.New("the password must be at most 72 bytes long (bcrypt limit)")
	}
	return nil
}

// HashPassword returns a bcrypt hash.
func HashPassword(password string) (string, error) {
	if err := ValidatePassword(password); err != nil {
		return "", err
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), BcryptCost)
	return string(h), err
}

// IsBcryptHash reports whether s looks like a bcrypt hash.
func IsBcryptHash(s string) bool {
	_, err := bcrypt.Cost([]byte(s))
	return err == nil
}

// Store is the subset of the monitoring store used by the service.
type Store interface {
	UserByUsername(ctx context.Context, username string) (repository.User, error)
	TouchLogin(ctx context.Context, id int64) error
	CreateSession(ctx context.Context, tokenHash []byte, userID int64, csrf string, expires time.Time) error
	SessionByHash(ctx context.Context, tokenHash []byte, idle time.Duration) (repository.AuthSession, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
}

// Service authenticates requests.
type Service struct {
	store        Store
	idle         time.Duration
	secureCookie bool
	limiter      *limiter
	// dummyHash keeps login timing similar for unknown users.
	dummyHash []byte
}

// NewService returns a service. idle is the session idle timeout.
func NewService(store Store, idle time.Duration, secureCookie bool) *Service {
	dummy, _ := bcrypt.GenerateFromPassword([]byte("locklens-timing-equaliser"), BcryptCost)
	return &Service{store: store, idle: idle, secureCookie: secureCookie, limiter: newLimiter(5, time.Minute), dummyHash: dummy}
}

// Session is an authenticated session.
type Session struct {
	UserID   int64
	Username string
	Role     Role
	// Managed is the environment administrator.
	Managed   bool
	CSRFToken string
	ExpiresAt time.Time
	tokenHash []byte
}

// Can reports whether the session's user has a permission.
func (s Session) Can(p Permission) bool { return s.Role.Can(p) }

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func hashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// Login checks credentials and creates a session, setting the cookie.
func (s *Service) Login(ctx context.Context, w http.ResponseWriter, clientKey, username, password string) (Session, error) {
	if !s.limiter.allow(clientKey) {
		return Session{}, ErrRateLimited
	}
	u, err := s.store.UserByUsername(ctx, username)
	switch {
	case errors.Is(err, repository.ErrNotFound):
		_ = bcrypt.CompareHashAndPassword(s.dummyHash, []byte(password))
		s.limiter.fail(clientKey)
		return Session{}, ErrInvalidCredentials
	case err != nil:
		return Session{}, err
	}
	// A disabled account answers like a wrong password, after the same
	// bcrypt work, so it cannot be told apart from an unknown user.
	if bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil || u.Disabled {
		s.limiter.fail(clientKey)
		return Session{}, ErrInvalidCredentials
	}
	s.limiter.reset(clientKey)
	token, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	csrf, err := randomToken()
	if err != nil {
		return Session{}, err
	}
	expires := time.Now().Add(s.idle)
	if err := s.store.CreateSession(ctx, hashToken(token), u.ID, csrf, expires); err != nil {
		return Session{}, err
	}
	_ = s.store.TouchLogin(ctx, u.ID)
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: token, Path: "/", HttpOnly: true, Secure: s.secureCookie,
		SameSite: http.SameSiteStrictMode, Expires: expires})
	return Session{UserID: u.ID, Username: u.Username, Role: Role(u.Role), Managed: u.Managed, CSRFToken: csrf, ExpiresAt: expires, tokenHash: hashToken(token)}, nil
}

// Authenticate returns the session of a request, or an error.
func (s *Service) Authenticate(r *http.Request) (Session, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" || len(c.Value) > 200 {
		return Session{}, repository.ErrNotFound
	}
	h := hashToken(c.Value)
	sess, err := s.store.SessionByHash(r.Context(), h, s.idle)
	if err != nil {
		return Session{}, err
	}
	return Session{UserID: sess.UserID, Username: sess.Username, Role: Role(sess.Role), Managed: sess.Managed,
		CSRFToken: sess.CSRFToken, ExpiresAt: sess.ExpiresAt, tokenHash: h}, nil
}

// Logout deletes the session and clears the cookie.
func (s *Service) Logout(ctx context.Context, w http.ResponseWriter, sess Session) error {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: s.secureCookie,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	return s.store.DeleteSession(ctx, sess.tokenHash)
}

// CheckCSRF validates the CSRF header and, when present, the Origin header
// against the request host. Safe methods are not checked.
func CheckCSRF(r *http.Request, sess Session) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	got := r.Header.Get(CSRFHeader)
	if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRFToken)) != 1 {
		return errors.New("missing or invalid CSRF token")
	}
	return CheckOrigin(r)
}

// CheckOrigin rejects cross-origin requests that carry an Origin header.
func CheckOrigin(r *http.Request) error {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return nil
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Host, r.Host) {
		return errors.New("cross-origin request rejected")
	}
	return nil
}

// limiter counts failed logins per client in a sliding window.
type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	fails  map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, fails: map[string][]time.Time{}}
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	kept := l.fails[key][:0]
	for _, t := range l.fails[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.fails, key)
		return nil
	}
	l.fails[key] = kept
	return kept
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.prune(key, time.Now())) < l.max
}

func (l *limiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.fails) > 10000 {
		l.fails = map[string][]time.Time{} // bound memory under a flood
	}
	l.fails[key] = append(l.prune(key, time.Now()), time.Now())
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.fails, key)
}

// VerifyPassword reports whether password matches a bcrypt hash.
func VerifyPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
