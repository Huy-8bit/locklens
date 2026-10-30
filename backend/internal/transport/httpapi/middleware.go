package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/auth"
	"github.com/locklens/locklens/internal/repository"
)

type ctxKey int

const (
	keySession ctxKey = iota
	keyRequestID
	keyToken
)

// ServeHTTP applies middleware: request ID, panic recovery, security
// headers, access logging, and request metrics.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	id := r.Header.Get("X-Request-ID")
	if id == "" || len(id) > 64 || strings.ContainsAny(id, "\r\n") {
		b := make([]byte, 8)
		_, _ = rand.Read(b)
		id = hex.EncodeToString(b)
	}
	w.Header().Set("X-Request-ID", id)
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	r = r.WithContext(context.WithValue(r.Context(), keyRequestID, id))
	rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
	defer func() {
		if p := recover(); p != nil {
			s.d.Log.Error("panic in handler", "request_id", id, "panic", p, "stack", string(debug.Stack()))
			writeError(rec, r, http.StatusInternalServerError, "internal", "internal error")
		}
		route := "ui"
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			route = "api"
		case r.URL.Path == "/metrics" || r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
			route = "ops"
		}
		s.d.Metrics.HTTPRequests.Inc(route, strconv.Itoa(rec.status/100)+"xx")
		if route != "ops" && !strings.HasSuffix(r.URL.Path, "/events") {
			s.d.Log.Debug("request", "request_id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration_ms", time.Since(start).Milliseconds())
		}
	}()
	s.mux.ServeHTTP(rec, r)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// authed requires a session and, for mutations, a valid CSRF token.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sess, err := s.d.Auth.Authenticate(r)
		if err != nil {
			if !errors.Is(err, repository.ErrNotFound) {
				s.internal(w, r, err)
				return
			}
			writeError(w, r, http.StatusUnauthorized, "unauthenticated", "login required")
			return
		}
		if err := auth.CheckCSRF(r, sess); err != nil {
			writeError(w, r, http.StatusForbidden, "csrf", err.Error())
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), keySession, sess)))
	}
}

// allow is authed plus a permission check. Missing permission answers 403.
func (s *Server) allow(p auth.Permission, h http.HandlerFunc) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request) {
		if !sessionOf(r).Can(p) {
			writeError(w, r, http.StatusForbidden, "forbidden", "your role does not allow this action")
			return
		}
		h(w, r)
	})
}

func sessionOf(r *http.Request) auth.Session {
	s, _ := r.Context().Value(keySession).(auth.Session)
	return s
}

func requestID(r *http.Request) string {
	id, _ := r.Context().Value(keyRequestID).(string)
	return id
}

// clientKey identifies the client for login rate limiting and logs. Behind
// a trusted reverse proxy it is the right-most X-Forwarded-For address that
// is not itself a trusted proxy; anything a client writes into the header
// further left is ignored.
func (s *Server) clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	addr, err := netip.ParseAddr(host)
	if err != nil || !s.trusted(addr) {
		return host
	}
	hops := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		hop, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		if !s.trusted(hop) {
			return hop.Unmap().String()
		}
	}
	return host
}

func (s *Server) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, p := range s.d.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	if len(s.d.TrustedProxyHosts) == 0 {
		return false
	}
	return s.proxyHostAddrs()[a]
}

// proxyHostAddrs resolves the trusted proxy host names at most every 30
// seconds. A failed lookup keeps the previous addresses.
func (s *Server) proxyHostAddrs() map[netip.Addr]bool {
	s.proxyMu.Lock()
	defer s.proxyMu.Unlock()
	if s.proxyAddrs != nil && time.Since(s.proxyResolved) < 30*time.Second {
		return s.proxyAddrs
	}
	lookup := s.d.LookupHost
	if lookup == nil {
		lookup = net.DefaultResolver.LookupHost
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addrs := map[netip.Addr]bool{}
	ok := false
	for _, h := range s.d.TrustedProxyHosts {
		list, err := lookup(ctx, h)
		if err != nil {
			continue
		}
		ok = true
		for _, a := range list {
			if ip, err := netip.ParseAddr(a); err == nil {
				addrs[ip.Unmap()] = true
			}
		}
	}
	if ok || s.proxyAddrs == nil {
		s.proxyAddrs = addrs
	}
	s.proxyResolved = time.Now()
	return s.proxyAddrs
}
