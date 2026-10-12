package web

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
)

// handlerFunc is the signature of authenticated handlers. Returned errors
// are rendered by the wrapper (HTML error page or JSON error).
type handlerFunc func(w http.ResponseWriter, r *http.Request, id auth.Identity) error

// page wraps an HTML page handler.
func (s *Server) page(min roles.Role, h handlerFunc) http.Handler {
	return s.authenticated(min, h, false)
}

// api wraps a JSON API handler.
func (s *Server) api(min roles.Role, h handlerFunc) http.Handler {
	return s.authenticated(min, h, true)
}

// authenticated resolves the identity, checks the role and the rate limit.
func (s *Server) authenticated(min roles.Role, h handlerFunc, isAPI bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		email, err := s.authn.Authenticate(r)
		if err != nil {
			slog.Warn("authentication failed", "path", r.URL.Path, "err", err)
			s.fail(w, r, isAPI, http.StatusUnauthorized, "You must be signed in with your company Google account.")
			return
		}
		id := auth.Identity{Email: email, Role: s.roles.RoleOf(email)}
		if !allowed(id.Role, min) {
			s.fail(w, r, isAPI, http.StatusForbidden, "You are not allowed to access this page.")
			return
		}
		// Anti-abuse: a token bucket per user. Combined with the pool cache
		// (singleflight + TTL) this bounds the calls sent to Google.
		if !s.limiter.Allow(email) {
			w.Header().Set("Retry-After", "30")
			s.fail(w, r, isAPI, http.StatusTooManyRequests, "Too many requests. Please slow down and retry in a few seconds.")
			return
		}
		r = r.WithContext(auth.WithIdentity(r.Context(), id))
		if lw, ok := w.(*statusWriter); ok {
			lw.user = email
		}
		if err := h(w, r, id); err != nil {
			var ue *service.UserError
			if errors.As(err, &ue) {
				s.fail(w, r, isAPI, ue.Status, ue.Msg)
				return
			}
			slog.Error("handler error", "path", r.URL.Path, "user", email, "err", err)
			s.fail(w, r, isAPI, http.StatusInternalServerError, "Something went wrong on our side. Please retry in a moment.")
		}
	})
}

func allowed(have, min roles.Role) bool {
	switch min {
	case roles.RoleSuperAdmin:
		return have.IsSuperAdmin()
	case roles.RoleAdmin:
		return have.IsAdmin()
	}
	return true
}

// fail writes an error as JSON (API) or as an HTML page.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, isAPI bool, status int, msg string) {
	if isAPI {
		writeJSON(w, status, map[string]string{"error": msg})
		return
	}
	s.renderError(w, r, status, msg)
}

// writeJSON writes a JSON response.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// readJSON decodes a JSON body (max 64 KiB, unknown fields rejected).
func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 64<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return &service.UserError{Status: http.StatusBadRequest, Msg: "Invalid request body."}
	}
	return nil
}

// --- Rate limiting ---------------------------------------------------------------

type rateLimiter struct {
	mu     sync.Mutex
	perSec rate.Limit
	burst  int
	byUser map[string]*limiterEntry
	lastGC time.Time
}

type limiterEntry struct {
	l    *rate.Limiter
	seen time.Time
}

func newRateLimiter(perMin, burst int) *rateLimiter {
	if perMin <= 0 {
		perMin = 120
	}
	if burst <= 0 {
		burst = 30
	}
	return &rateLimiter{perSec: rate.Limit(float64(perMin) / 60), burst: burst, byUser: map[string]*limiterEntry{}, lastGC: time.Now()}
}

// Allow consumes one token for a user.
func (l *rateLimiter) Allow(user string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	if now.Sub(l.lastGC) > 10*time.Minute {
		for k, e := range l.byUser {
			if now.Sub(e.seen) > 10*time.Minute {
				delete(l.byUser, k)
			}
		}
		l.lastGC = now
	}
	e, ok := l.byUser[user]
	if !ok {
		e = &limiterEntry{l: rate.NewLimiter(l.perSec, l.burst)}
		l.byUser[user] = e
	}
	e.seen = now
	return e.l.Allow()
}

// --- Generic middlewares -----------------------------------------------------------

// statusWriter captures the status code for the access log.
type statusWriter struct {
	http.ResponseWriter
	status int
	user   string
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logging writes one structured access log line per request.
func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/static/") || r.URL.Path == "/healthz" {
			return
		}
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(), "user", sw.user)
	})
}

// securityHeaders sets a strict Content Security Policy and friends. No
// inline script is used anywhere; page data travels in JSON script blocks.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	csp := strings.Join([]string{
		"default-src 'self'",
		"script-src 'self'",
		"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com",
		"font-src 'self' https://fonts.gstatic.com",
		"img-src 'self' data:",
		"connect-src 'self'",
		"frame-ancestors 'none'",
		"base-uri 'self'",
		"form-action 'self'",
	}, "; ")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if !s.cfg.IsDev() {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// recoverer turns a panic into a 500 and logs the stack.
func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				slog.Error("panic", "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// cacheStatic sets long-lived cache headers on static assets (they are
// versioned with a query string carrying the build version).
func cacheStatic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		next.ServeHTTP(w, r)
	})
}
