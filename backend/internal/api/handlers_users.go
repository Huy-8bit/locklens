package api

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/auth"
	"github.com/locklens/locklens/internal/storage"
)

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@-]{0,63}$`)

type userView struct {
	ID          int64      `json:"id"`
	Username    string     `json:"username"`
	Role        string     `json:"role"`
	Disabled    bool       `json:"disabled"`
	Managed     bool       `json:"managed"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastLoginAt *time.Time `json:"last_login_at"`
}

func toUserView(u storage.User) userView {
	return userView{ID: u.ID, Username: u.Username, Role: u.Role, Disabled: u.Disabled, Managed: u.Managed,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt, LastLoginAt: u.LastLoginAt}
}

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.d.Store.ListUsers(r.Context())
	if err != nil {
		s.internal(w, r, err)
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, toUserView(u))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "roles": auth.Roles})
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Role     string `json:"role"`
		Password string `json:"password"`
	}
	if err := decode(w, r, &body, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	fields := map[string]string{}
	if !usernamePattern.MatchString(body.Username) {
		fields["username"] = "1–64 characters: letters, digits, '.', '_', '@', or '-', starting with a letter or digit"
	}
	if !auth.ValidRole(body.Role) {
		fields["role"] = "admin, operator, or viewer"
	}
	if err := auth.ValidatePassword(body.Password); err != nil {
		fields["password"] = err.Error()
	}
	if len(fields) > 0 {
		writeFieldErrors(w, r, fields)
		return
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	u, err := s.d.Store.CreateUser(r.Context(), body.Username, hash, body.Role)
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, r, http.StatusConflict, "conflict", "a user with this name already exists")
		return
	}
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("user created", "user", u.Username, "role", u.Role, "by", sessionOf(r).Username)
	writeJSON(w, http.StatusCreated, toUserView(u))
}

func (s *Server) userFromPath(w http.ResponseWriter, r *http.Request) (storage.User, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return storage.User{}, false
	}
	u, err := s.d.Store.UserByID(r.Context(), id)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, r, http.StatusNotFound, "not_found", "user not found")
		return u, false
	}
	if err != nil {
		s.internal(w, r, err)
		return u, false
	}
	return u, true
}

// updateUser changes a user's role, enabled state, or password. The
// environment administrator is changed only through the environment, and
// nobody can lower their own role or disable themselves (so an admin cannot
// lock everyone out by accident).
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if err := decode(w, r, &body, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if u.Managed {
		writeError(w, r, http.StatusConflict, "managed_user", "this administrator is configured by LOCKLENS_ADMIN_USER and LOCKLENS_ADMIN_PASSWORD; change it there")
		return
	}
	self := u.ID == sessionOf(r).UserID
	fields := map[string]string{}
	if body.Role != nil && !auth.ValidRole(*body.Role) {
		fields["role"] = "admin, operator, or viewer"
	}
	if self && body.Role != nil && *body.Role != u.Role {
		fields["role"] = "you cannot change your own role"
	}
	if self && body.Disabled != nil && *body.Disabled {
		fields["disabled"] = "you cannot disable your own account"
	}
	var hash *string
	if body.Password != nil {
		if err := auth.ValidatePassword(*body.Password); err != nil {
			fields["password"] = err.Error()
		} else if h, err := auth.HashPassword(*body.Password); err == nil {
			hash = &h
		} else {
			s.internal(w, r, err)
			return
		}
	}
	if len(fields) > 0 {
		writeFieldErrors(w, r, fields)
		return
	}
	out, err := s.d.Store.UpdateUser(r.Context(), u.ID, storage.UserChanges{Role: body.Role, Disabled: body.Disabled, PasswordHash: hash})
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("user updated", "user", out.Username, "role", out.Role, "disabled", out.Disabled, "password_reset", hash != nil, "by", sessionOf(r).Username)
	writeJSON(w, http.StatusOK, toUserView(out))
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) {
	u, ok := s.userFromPath(w, r)
	if !ok {
		return
	}
	switch {
	case u.Managed:
		writeError(w, r, http.StatusConflict, "managed_user", "this administrator is configured by the environment and cannot be deleted")
		return
	case u.ID == sessionOf(r).UserID:
		writeError(w, r, http.StatusConflict, "self", "you cannot delete your own account")
		return
	}
	if err := s.d.Store.DeleteUser(r.Context(), u.ID); err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("user deleted", "user", u.Username, "by", sessionOf(r).Username)
	w.WriteHeader(http.StatusNoContent)
}

// changeOwnPassword lets any user change their password with the current
// one. Their other sessions end; this one is replaced by a fresh login.
func (s *Server) changeOwnPassword(w http.ResponseWriter, r *http.Request) {
	sess := sessionOf(r)
	var body struct {
		Current string `json:"current_password"`
		New     string `json:"new_password"`
	}
	if err := decode(w, r, &body, 4096); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if sess.Managed {
		writeError(w, r, http.StatusConflict, "managed_user", "this administrator's password is set by LOCKLENS_ADMIN_PASSWORD; change it there and restart")
		return
	}
	u, err := s.d.Store.UserByID(r.Context(), sess.UserID)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if !auth.VerifyPassword(u.PasswordHash, body.Current) {
		writeFieldErrors(w, r, map[string]string{"current_password": "the current password is not correct"})
		return
	}
	if err := auth.ValidatePassword(body.New); err != nil {
		writeFieldErrors(w, r, map[string]string{"new_password": err.Error()})
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	if _, err := s.d.Store.UpdateUser(r.Context(), u.ID, storage.UserChanges{PasswordHash: &hash}); err != nil {
		s.internal(w, r, err)
		return
	}
	// UpdateUser ended every session of this user, including this one; sign
	// in again with the new password.
	ns, err := s.d.Auth.Login(r.Context(), w, clientKey(r), u.Username, body.New)
	if err != nil {
		s.internal(w, r, err)
		return
	}
	s.d.Log.Info("password changed", "user", u.Username)
	writeJSON(w, http.StatusOK, s.sessionPayload(ns))
}
