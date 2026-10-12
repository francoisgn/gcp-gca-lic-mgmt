// Package web serves the portal: server-rendered pages (html/template), a
// small JSON API consumed by vanilla JavaScript, and static assets embedded
// in the binary. No front-end build chain on purpose.
package web

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/jobs"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
)

//go:embed templates/*.html
var templatesFS embed.FS

//go:embed static
var staticFS embed.FS

// Server holds the HTTP layer.
type Server struct {
	cfg     *config.Config
	svc     *service.Service
	runner  *jobs.Runner
	roles   *roles.Roles
	authn   auth.Authenticator
	pages   map[string]*pageTemplate
	limiter *rateLimiter
	version string
}

// New builds the server and parses the templates.
func New(cfg *config.Config, svc *service.Service, runner *jobs.Runner, rl *roles.Roles, authn auth.Authenticator, version string) (*Server, error) {
	s := &Server{cfg: cfg, svc: svc, runner: runner, roles: rl, authn: authn, version: version,
		limiter: newRateLimiter(cfg.RateLimitPerMin, cfg.RateLimitBurst)}
	var err error
	if s.pages, err = parsePages(cfg); err != nil {
		return nil, err
	}
	return s, nil
}

// Handler returns the full HTTP handler (portal and/or jobs depending on the mode).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	if s.cfg.ServesJobs() {
		mux.Handle("POST /jobs/", s.runner.Handler(s.cfg.JobsAudience, s.cfg.JobsInvoker))
	}
	if s.cfg.ServesWeb() {
		s.portalRoutes(mux)
	}

	// Cross-origin protection (CSRF) for every state-changing request: the
	// browser's Sec-Fetch-Site / Origin headers must match our origin.
	// Scheduler calls (no browser headers) are not affected.
	csrf := http.NewCrossOriginProtection()
	return chain(mux, csrf.Handler, s.securityHeaders, s.logging, recoverer)
}

func (s *Server) portalRoutes(mux *http.ServeMux) {
	static, _ := fs.Sub(staticFS, "static")
	mux.Handle("GET /static/", cacheStatic(http.StripPrefix("/static/", http.FileServerFS(static))))
	mux.Handle("GET /favicon.ico", http.RedirectHandler("/static/img/favicon.svg", http.StatusMovedPermanently))

	// Pages.
	mux.Handle("GET /{$}", s.page(roles.RoleUser, s.homePage))
	mux.Handle("GET /feedback", s.page(roles.RoleUser, s.feedbackPage))
	mux.Handle("GET /admin", s.page(roles.RoleAdmin, s.adminPage))
	mux.Handle("GET /observability", s.page(roles.RoleAdmin, s.observabilityPage))

	// User API.
	mux.Handle("POST /api/request/directory", s.api(roles.RoleUser, s.apiDirectory))
	mux.Handle("POST /api/request/{id}/submit", s.api(roles.RoleUser, s.apiSubmit))
	mux.Handle("POST /api/request/{id}/escalate", s.api(roles.RoleUser, s.apiEscalate))
	mux.Handle("POST /api/request/{id}/exit", s.api(roles.RoleUser, s.apiExit))
	mux.Handle("POST /api/queue/leave", s.api(roles.RoleUser, s.apiLeaveQueue))
	mux.Handle("POST /api/feedback", s.api(roles.RoleUser, s.apiFeedback))
	mux.Handle("GET /api/counter", s.api(roles.RoleUser, s.apiCounter))

	// Admin API.
	mux.Handle("POST /api/admin/requests/{id}/approve", s.api(roles.RoleAdmin, s.apiApprove))
	mux.Handle("POST /api/admin/requests/{id}/reject", s.api(roles.RoleAdmin, s.apiReject))
	mux.Handle("POST /api/admin/assign", s.api(roles.RoleAdmin, s.apiAssign))
	mux.Handle("POST /api/admin/transfer", s.api(roles.RoleAdmin, s.apiTransfer))
	mux.Handle("POST /api/admin/unassign", s.api(roles.RoleAdmin, s.apiUnassign))
	mux.Handle("POST /api/admin/unassign-by-filter", s.api(roles.RoleAdmin, s.apiUnassignByFilter))
	mux.Handle("POST /api/admin/inventory", s.api(roles.RoleAdmin, s.apiInventory))
	mux.Handle("POST /api/admin/queue/{id}/cancel", s.api(roles.RoleAdmin, s.apiCancelQueue))
	mux.Handle("POST /api/admin/buffer/{id}/retry", s.api(roles.RoleAdmin, s.apiRetryBuffer))

	// Observability API (admins).
	mux.Handle("GET /api/obs/summary", s.api(roles.RoleAdmin, s.apiObsSummary))
	mux.Handle("GET /api/obs/flows", s.api(roles.RoleAdmin, s.apiObsFlows))
	mux.Handle("GET /api/obs/depths", s.api(roles.RoleAdmin, s.apiObsDepths))
	mux.Handle("GET /api/obs/tech", s.api(roles.RoleAdmin, s.apiObsTech))
	mux.Handle("GET /api/obs/deviations", s.api(roles.RoleAdmin, s.apiObsDeviations))
	mux.Handle("POST /api/obs/deviations/{id}/resolve", s.api(roles.RoleAdmin, s.apiResolveDeviation))

	// Super admin API.
	mux.Handle("POST /api/super/refresh", s.api(roles.RoleSuperAdmin, s.apiForceRefresh))
	mux.Handle("POST /api/super/jobs/{name}", s.api(roles.RoleSuperAdmin, s.apiRunJob))
	mux.Handle("POST /api/super/sim/rollover", s.api(roles.RoleSuperAdmin, s.apiSimRollover))
	mux.Handle("POST /api/super/sim/seats", s.api(roles.RoleSuperAdmin, s.apiSimSeats))
	mux.Handle("POST /api/super/sim/remove", s.api(roles.RoleSuperAdmin, s.apiSimRemove))

	// Development identity switcher.
	if s.cfg.AuthMode == "dev" {
		mux.HandleFunc("POST /dev/login", s.devLogin)
	}
}

// chain applies middlewares, the first one being the innermost.
func chain(h http.Handler, mws ...func(http.Handler) http.Handler) http.Handler {
	for _, mw := range mws {
		h = mw(h)
	}
	return h
}
