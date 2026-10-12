// Package testenv builds a fully wired service on top of a real PostgreSQL
// database and the fake Google / directory adapters, for integration tests.
//
// Tests using it are skipped unless TEST_DATABASE_URL is set, e.g.
//
//	TEST_DATABASE_URL=postgres://gcalm:gcalm@localhost:5432/gcalm_test?sslmode=disable go test ./...
//
// Every New call truncates all tables: tests using testenv must not run in
// parallel.
package testenv

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/mail"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/policy"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/tracker"
)

// RolesYAML is the roles file used by tests.
const RolesYAML = `
super_admins: [super.admin@example.com]
admins: [alex.martin@example.com, sam.lee@example.com]
`

// PolicyYAML is the policy file used by tests.
const PolicyYAML = `
accounts: {external_suffix: ".ext"}
durations: [{code: 7d}, {code: 1M}, {code: 3M}]
default_duration: 1M
unused_days: 7
buffer_max_attempts: 5
questions:
  - {id: use_case, label: "Main use case", type: select, required: true, options: [Dev, Data]}
  - {id: justification, label: "Business justification", type: textarea, required: true, min_length: 10}
links: {training: "https://learning.example.com/gca"}
`

// Env is a wired test environment.
type Env struct {
	Cfg    *config.Config
	DB     *store.DB
	Svc    *service.Service
	Fake   *gcp.FakeLicensing
	Mails  *CaptureMailer
	Clock  *Clock
	Roles  *roles.Roles
	Policy *policy.Policy
}

// Option customises the configuration.
type Option func(*config.Config)

// New builds an environment with a pool of `seats` licenses.
func New(t *testing.T, seats int, opts ...Option) *Env {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set: integration test skipped")
	}
	ctx := context.Background()
	db, err := store.Open(ctx, url)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := db.Pool.Exec(ctx, `TRUNCATE subscriptions, license_requests, license_assignments, queue_entries,
		buffer_entries, deviations, api_calls, daily_stats, feedback, audit_log, job_runs, allowed_gps_codes,
		notifications RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	cfg := &config.Config{
		Mode: "all", Env: "dev", BaseURL: "http://portal.test", TimeZone: time.UTC, AuthMode: "dev",
		GoogleMode: "fake", CredentialsMode: "fake", DirectoryMode: "simulated", MailMode: "log", TrackerMode: "log",
		OrderMatch:       regexp.MustCompile(`(?i)gemini|code ?assist|cloudaicompanion`),
		DirectoryTimeout: 300 * time.Millisecond, UXTimeout: 400 * time.Millisecond, JobCallTimeout: 600 * time.Millisecond,
		PoolCacheTTL: 0, MetricsRetention: 10 * 24 * time.Hour, BufferRetryEvery: 7 * time.Minute,
		RateLimitPerMin: 1000, RateLimitBurst: 1000, GCAProjectID: "test-project",
	}
	for _, o := range opts {
		o(cfg)
	}
	rl, err := roles.Parse([]byte(RolesYAML))
	if err != nil {
		t.Fatal(err)
	}
	pol, err := policy.Parse([]byte(PolicyYAML))
	if err != nil {
		t.Fatal(err)
	}
	fake, err := gcp.NewFakeLicensing(filepath.Join(t.TempDir(), "fake.json"), seats, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	renderer, err := mail.NewRenderer(time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	rec := telemetry.NewRecorder(db.Q)
	clock := &Clock{}
	mails := &CaptureMailer{}
	svc := service.New(service.Deps{
		Cfg: cfg, DB: db, Roles: rl, Policy: pol, Recorder: rec, Fake: fake,
		Licensing:   gcp.InstrumentLicensing(fake, rec),
		Credentials: gcp.InstrumentCredentials(&gcp.FakeCredentials{}, rec),
		Directory:   directory.Instrument(&directory.Simulator{}, rec),
		Mailer:      mails, Mails: renderer, Tracker: tracker.Log{},
		Now: clock.Now,
	})
	if _, err := svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatalf("initial refresh: %v", err)
	}
	// Every GPS code of the simulator is authorized unless a test removes it.
	if _, err := db.AddGPSCodes(ctx, directory.SimulatorGPSCodes, "test", "super.admin@example.com"); err != nil {
		t.Fatalf("gps allowlist: %v", err)
	}
	return &Env{Cfg: cfg, DB: db, Svc: svc, Fake: fake, Mails: mails, Clock: clock, Roles: rl, Policy: pol}
}

// User returns the identity of an e-mail with its role.
func (e *Env) User(email string) auth.Identity {
	return auth.Identity{Email: email, Role: e.Roles.RoleOf(email)}
}

// Form returns a valid form input.
func Form() service.FormInput {
	return service.FormInput{TrainingAck: true, Duration: "1M",
		Answers: map[string]string{"use_case": "Dev", "justification": "Faster unit tests please"}}
}

// Request runs the directory step and the submission for a user.
func (e *Env) Request(t *testing.T, email string) (*service.DirectoryResult, *service.FulfilResult) {
	t.Helper()
	ctx := context.Background()
	dir, err := e.Svc.CheckDirectory(ctx, e.User(email), Form())
	if err != nil {
		t.Fatalf("CheckDirectory(%s): %v", email, err)
	}
	if dir.Outcome != "OK" {
		return dir, nil
	}
	res, err := e.Svc.Submit(ctx, e.User(email), dir.RequestID)
	if err != nil {
		t.Fatalf("Submit(%s): %v", email, err)
	}
	return dir, res
}

// Clock is the real clock shifted by a controllable offset (the fake Google
// API uses the real time, so the service clock must keep moving too).
type Clock struct {
	mu     sync.Mutex
	offset time.Duration
}

// Now returns the shifted time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Now().Add(c.offset)
}

// Advance moves the clock forward.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.offset += d
	c.mu.Unlock()
}

// CaptureMailer records sent messages.
type CaptureMailer struct {
	mu   sync.Mutex
	Msgs []mail.Message
}

// Send implements mail.Mailer.
func (m *CaptureMailer) Send(_ context.Context, msg mail.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Msgs = append(m.Msgs, msg)
	return nil
}

// Count returns the number of messages sent to an address with a subject
// containing the given text.
func (m *CaptureMailer) Count(to, subject string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, msg := range m.Msgs {
		if msg.To == to && strings.Contains(msg.Subject, subject) {
			n++
		}
	}
	return n
}
