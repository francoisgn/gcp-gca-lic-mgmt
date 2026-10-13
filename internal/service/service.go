// Package service implements the business rules of the license portal:
// request workflow, automatic / admin assignment, waiting list (queue),
// buffer of timed-out operations, purges, subscription rollover, deviations
// with Google and KPIs. It is independent from HTTP: the web layer and the
// scheduled jobs both call it.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/mail"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/policy"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/tracker"
)

// Deps groups the dependencies of the service.
type Deps struct {
	Cfg         *config.Config
	DB          *store.DB
	Roles       *roles.Roles
	Policy      *policy.Policy
	Licensing   gcp.Licensing
	Credentials gcp.Credentials
	Directory   directory.Client
	Mailer      mail.Mailer
	Mails       *mail.Renderer
	Tracker     tracker.Tracker
	Recorder    *telemetry.Recorder
	// Fake is set when GOOGLE_MODE=fake: enables the simulation panel.
	Fake *gcp.FakeLicensing
	// Now is the clock (overridable in tests).
	Now func() time.Time
}

// Service is the business facade.
type Service struct {
	Deps
	pool      *poolCache
	queueKick chan struct{}
}

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	s := &Service{Deps: d, queueKick: make(chan struct{}, 1)}
	s.pool = &poolCache{s: s, ttl: d.Cfg.PoolCacheTTL}
	return s
}

// QueueKicks is signalled when seats may have been released. A background
// goroutine (see cmd/gcalm) runs the queue job on each signal; the scheduled
// job remains the safety net.
func (s *Service) QueueKicks() <-chan struct{} { return s.queueKick }

// kickQueue requests an asynchronous queue processing (non-blocking).
func (s *Service) kickQueue() {
	select {
	case s.queueKick <- struct{}{}:
	default:
	}
}

// =============================================================================
// Errors returned to the web layer
// =============================================================================

// UserError is an error whose message can be shown to the user.
type UserError struct {
	Status int
	Msg    string
}

func (e *UserError) Error() string { return e.Msg }

func badRequest(format string, a ...any) error {
	return &UserError{Status: http.StatusBadRequest, Msg: fmt.Sprintf(format, a...)}
}
func conflict(format string, a ...any) error {
	return &UserError{Status: http.StatusConflict, Msg: fmt.Sprintf(format, a...)}
}
func notFound(format string, a ...any) error {
	return &UserError{Status: http.StatusNotFound, Msg: fmt.Sprintf(format, a...)}
}
func forbidden(format string, a ...any) error {
	return &UserError{Status: http.StatusForbidden, Msg: fmt.Sprintf(format, a...)}
}

// =============================================================================
// License pool snapshot cache
// =============================================================================

// PoolSnapshot is what Google says about the current pool. It is the source
// of truth for "does this user hold a license".
type PoolSnapshot struct {
	Subscription *store.Subscription
	Pool         gcp.Pool
	Users        map[string]gcp.LicensedUser
	FetchedAt    time.Time
	Stale        bool // Google failed, an older snapshot is served
}

// Has reports whether a user holds a license in the snapshot.
func (p *PoolSnapshot) Has(email string) (gcp.LicensedUser, bool) {
	if p == nil {
		return gcp.LicensedUser{}, false
	}
	u, ok := p.Users[email]
	return u, ok
}

// poolCache protects the Google API from page refresh storms: at most one
// enumeration per TTL whatever the number of users (singleflight + TTL).
// Each refresh is recorded as api_calls (GOOGLE_LICENSE getPool/enumerate),
// which feeds the "app access vs Google checks" KPI.
type poolCache struct {
	s    *Service
	ttl  time.Duration
	mu   sync.Mutex
	snap *PoolSnapshot
	sf   singleflight.Group
}

// Get returns a snapshot younger than the TTL (fetching it if needed).
func (c *poolCache) Get(ctx context.Context) (*PoolSnapshot, error) {
	c.mu.Lock()
	snap := c.snap
	c.mu.Unlock()
	if snap != nil && time.Since(snap.FetchedAt) < c.ttl {
		return snap, nil
	}
	v, err, _ := c.sf.Do("pool", func() (any, error) {
		// Detached context: the result is shared by every waiting caller,
		// one cancelled request must not fail the others.
		fctx, cancel := context.WithTimeout(telemetry.WithKind(context.WithoutCancel(ctx), telemetry.KindFromOr(ctx, telemetry.KindUX)), 15*time.Second)
		defer cancel()
		return c.fetch(fctx)
	})
	if err != nil {
		if snap != nil {
			stale := *snap
			stale.Stale = true
			slog.Warn("serving stale pool snapshot", "err", err)
			return &stale, nil
		}
		return nil, err
	}
	return v.(*PoolSnapshot), nil
}

func (c *poolCache) fetch(ctx context.Context) (*PoolSnapshot, error) {
	sub, err := c.s.DB.CurrentSubscription(ctx)
	if errors.Is(err, store.ErrNotFound) {
		// First start: discover the subscriptions synchronously.
		if _, rerr := c.s.RefreshSubscriptions(ctx); rerr != nil {
			return nil, rerr
		}
		sub, err = c.s.DB.CurrentSubscription(ctx)
	}
	if err != nil {
		return nil, err
	}
	pool, err := c.s.Licensing.GetPool(ctx, sub.ID)
	if err != nil {
		return nil, err
	}
	users, err := c.s.Licensing.Enumerate(ctx, sub.ID)
	if err != nil {
		return nil, err
	}
	snap := &PoolSnapshot{Subscription: sub, Pool: pool, Users: make(map[string]gcp.LicensedUser, len(users)), FetchedAt: time.Now()}
	for _, u := range users {
		snap.Users[u.Email] = u
	}
	c.mu.Lock()
	c.snap = snap
	c.mu.Unlock()
	return snap, nil
}

// Invalidate forces the next Get to call Google.
func (c *poolCache) Invalidate() {
	c.mu.Lock()
	c.snap = nil
	c.mu.Unlock()
}

// PoolCounter is the "assigned / total" pill of the top bar.
type PoolCounter struct {
	Assigned  int  `json:"assigned"`
	Total     int  `json:"total"`
	Available int  `json:"available"`
	Reserved  int  `json:"reserved"` // seats reserved by in-flight or buffered requests
	Known     bool `json:"known"`    // false when Google could not be reached
	Stale     bool `json:"stale"`
}

// Counter returns the top bar counter.
func (s *Service) Counter(ctx context.Context) PoolCounter {
	snap, err := s.pool.Get(ctx)
	if err != nil || snap == nil {
		return PoolCounter{}
	}
	reserved, _ := s.DB.ReservedSeats(ctx, 0)
	return PoolCounter{
		Assigned: snap.Pool.Assigned(), Total: snap.Pool.Total, Available: snap.Pool.Available,
		Reserved: reserved, Known: true, Stale: snap.Stale,
	}
}

// RecordPageView increments the daily page view counter (best effort).
func (s *Service) RecordPageView(ctx context.Context) {
	if err := s.DB.IncPageViews(ctx, s.Now().In(s.Cfg.TimeZone)); err != nil {
		slog.Warn("page view counter", "err", err)
	}
}

// --- small helpers -------------------------------------------------------------

func ptr[T any](v T) *T { return &v }

func strOr(p *string, def string) string {
	if p == nil {
		return def
	}
	return *p
}

// detached returns a context that survives the caller's cancellation, with a
// timeout. Used to persist the outcome of an operation whose ctx expired.
func detached(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), d)
}

// jobCtx returns a context for one external operation inside a job.
func (s *Service) jobCtx(ctx context.Context, kind telemetry.Kind) (context.Context, context.CancelFunc) {
	return context.WithTimeout(telemetry.WithKind(ctx, kind), s.Cfg.JobCallTimeout)
}
