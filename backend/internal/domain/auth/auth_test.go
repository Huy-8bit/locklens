package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/locklens/locklens/internal/repository"
)

type memStore struct {
	mu       sync.Mutex
	user     repository.User
	sessions map[string]repository.AuthSession
}

func (m *memStore) UserByUsername(_ context.Context, u string) (repository.User, error) {
	if !strings.EqualFold(u, m.user.Username) {
		return repository.User{}, repository.ErrNotFound
	}
	return m.user, nil
}
func (m *memStore) TouchLogin(context.Context, int64) error { return nil }
func (m *memStore) CreateSession(_ context.Context, h []byte, id int64, csrf string, exp time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(h)] = repository.AuthSession{UserID: id, Username: m.user.Username, Role: m.user.Role, CSRFToken: csrf, ExpiresAt: exp}
	return nil
}
func (m *memStore) SessionByHash(_ context.Context, h []byte, _ time.Duration) (repository.AuthSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[string(h)]
	if !ok || time.Now().After(s.ExpiresAt) {
		return s, repository.ErrNotFound
	}
	return s, nil
}
func (m *memStore) DeleteSession(_ context.Context, h []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(h))
	return nil
}

func newTestService(t *testing.T) (*Service, *memStore) {
	t.Helper()
	hash, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	st := &memStore{user: repository.User{ID: 1, Username: "admin", PasswordHash: hash, Role: "admin"}, sessions: map[string]repository.AuthSession{}}
	return NewService(st, time.Hour, true), st
}

func TestLoginSessionLogout(t *testing.T) {
	s, st := newTestService(t)
	rec := httptest.NewRecorder()
	sess, err := s.Login(context.Background(), rec, "1.2.3.4", "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	cookie := rec.Result().Cookies()[0]
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteStrictMode || cookie.Name != CookieName {
		t.Errorf("cookie flags: %+v", cookie)
	}
	for h := range st.sessions {
		if strings.Contains(h, cookie.Value) {
			t.Fatal("the store must hold a hash, not the token")
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	req.AddCookie(cookie)
	got, err := s.Authenticate(req)
	if err != nil || got.Username != "admin" || got.CSRFToken != sess.CSRFToken {
		t.Fatalf("authenticate: %+v %v", got, err)
	}
	if err := s.Logout(context.Background(), httptest.NewRecorder(), got); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(req); err == nil {
		t.Fatal("session must be gone after logout")
	}
}

func TestLoginFailuresAndRateLimit(t *testing.T) {
	s, _ := newTestService(t)
	for i := 0; i < 5; i++ {
		_, err := s.Login(context.Background(), httptest.NewRecorder(), "9.9.9.9", "admin", "wrong password!!")
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := s.Login(context.Background(), httptest.NewRecorder(), "9.9.9.9", "admin", "correct horse battery"); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th attempt must be rate limited even with the right password: %v", err)
	}
	if _, err := s.Login(context.Background(), httptest.NewRecorder(), "8.8.8.8", "nobody", "x"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal("unknown user must look like a wrong password")
	}
}

func TestCSRFAndOrigin(t *testing.T) {
	sess := Session{CSRFToken: "tok"}
	get := httptest.NewRequest(http.MethodGet, "http://localhost:8080/x", nil)
	if CheckCSRF(get, sess) != nil {
		t.Error("GET is safe")
	}
	post := httptest.NewRequest(http.MethodPost, "http://localhost:8080/x", nil)
	if CheckCSRF(post, sess) == nil {
		t.Error("POST without token must fail")
	}
	post.Header.Set(CSRFHeader, "tok")
	if err := CheckCSRF(post, sess); err != nil {
		t.Errorf("valid token: %v", err)
	}
	post.Header.Set("Origin", "http://evil.example")
	if CheckCSRF(post, sess) == nil {
		t.Error("cross-origin must fail")
	}
	post.Header.Set("Origin", "http://localhost:8080")
	if err := CheckCSRF(post, sess); err != nil {
		t.Errorf("same origin: %v", err)
	}
}

func TestPasswordPolicy(t *testing.T) {
	if _, err := HashPassword("short"); err == nil {
		t.Error("short passwords must be rejected")
	}
	h, err := HashPassword("a long enough password")
	if err != nil || !IsBcryptHash(h) {
		t.Fatalf("hash: %v", err)
	}
	if IsBcryptHash("plaintext") {
		t.Error("plaintext is not a hash")
	}
}

func TestDisabledUserCannotLogIn(t *testing.T) {
	svc, st := newTestService(t)
	st.user.Disabled = true
	_, err := svc.Login(context.Background(), httptest.NewRecorder(), "client", "admin", "correct horse battery")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("disabled user: err = %v, want invalid credentials", err)
	}
}

func TestLoginCarriesRole(t *testing.T) {
	svc, st := newTestService(t)
	st.user.Role = "operator"
	sess, err := svc.Login(context.Background(), httptest.NewRecorder(), "client", "ADMIN", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if sess.Role != RoleOperator || sess.UserID != 1 || !sess.Can(PermRunSQL) || sess.Can(PermManageUsers) {
		t.Fatalf("session = %+v", sess)
	}
}

func TestRolePermissions(t *testing.T) {
	cases := []struct {
		role Role
		perm Permission
		want bool
	}{
		{RoleViewer, PermView, true},
		{RoleViewer, PermBrowseStructure, true},
		{RoleViewer, PermBrowseData, false},
		{RoleViewer, PermRunSQL, false},
		{RoleViewer, PermAcknowledge, false},
		{RoleViewer, PermManageTargets, false},
		{RoleOperator, PermBrowseData, true},
		{RoleOperator, PermRunSQL, true},
		{RoleOperator, PermAcknowledge, true},
		{RoleOperator, PermManageTargets, false},
		{RoleOperator, PermManageSettings, false},
		{RoleOperator, PermManageUsers, false},
		{RoleAdmin, PermManageTargets, true},
		{RoleAdmin, PermManageSettings, true},
		{RoleAdmin, PermManageUsers, true},
		{Role("root"), PermView, false},
	}
	for _, c := range cases {
		if got := c.role.Can(c.perm); got != c.want {
			t.Errorf("%s can %s = %v, want %v", c.role, c.perm, got, c.want)
		}
	}
	if !ValidRole("viewer") || ValidRole("superuser") {
		t.Error("ValidRole")
	}
}
