// Package config loads the runtime configuration from environment variables
// (12-factor style). Secrets are injected as environment variables by Cloud
// Run from Secret Manager; nothing secret is ever read from a file in git.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is the full runtime configuration of the portal.
type Config struct {
	// Mode selects what the process serves: "web" (portal behind IAP),
	// "worker" (scheduled jobs endpoints) or "all" (both, for local dev).
	Mode string
	Port string
	// Env is "dev" or "prod". Dev unlocks the identity switcher and the
	// simulation panel.
	Env      string
	BaseURL  string // public URL of the portal, used in e-mails
	TimeZone *time.Location

	DatabaseURL string

	// Authentication.
	AuthMode       string // "iap" or "dev"
	IAPAudience    string // /projects/NUMBER/locations/REGION/services/SERVICE (Cloud Run IAP)
	DevDefaultUser string

	RolesFile  string
	PolicyFile string

	// Google license management (Cloud Commerce Consumer Procurement API).
	GoogleMode     string // "real" or "fake"
	BillingAccount string // billing account ID that owns the Gemini Code Assist orders
	QuotaProject   string // project billed for API quota (X-Goog-User-Project)
	OrderMatch     *regexp.Regexp
	OrderIDs       []string // optional explicit order IDs, bypasses OrderMatch
	FakeStateFile  string
	FakeSeats      int
	FakeMaxLatency time.Duration
	FakeHangRate   float64 // probability for the fake Google API to hang (simulates timeouts)
	// GCAProjectID is the Google Cloud project users select in their IDE
	// (where the Gemini for Google Cloud API is enabled). Shown in e-mails.
	GCAProjectID string
	// DemoSeed fills an empty database with demo data (dev only).
	DemoSeed bool

	// Cloud credentials (IAM bundle) granted alongside the license.
	CredentialsMode  string // "group", "none" or "fake"
	CredentialsGroup string // e-mail of the Google group bound to the IAM roles

	// Enterprise directory.
	DirectoryMode    string // "http" or "simulated"
	DirectoryURL     string
	DirectoryAPIKey  string
	DirectoryTimeout time.Duration
	SimFailureRate   float64
	SimMaxLatency    time.Duration

	// E-mail.
	MailMode     string // "smtp" or "log"
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	MailFrom     string

	// Feedback tracker.
	TrackerMode   string // "jira", "gitlab" or "log"
	JiraURL       string
	JiraUser      string
	JiraToken     string
	JiraProject   string
	JiraIssueType string
	GitLabURL     string
	GitLabToken   string
	GitLabProject string

	// Timeouts, caches and limits.
	UXTimeout        time.Duration // budget for credentials + license during the form submission
	JobCallTimeout   time.Duration // per external call budget inside scheduled jobs
	PoolCacheTTL     time.Duration // cache of the Google license pool snapshot
	RateLimitPerMin  int
	RateLimitBurst   int
	MetricsRetention time.Duration
	BufferRetryEvery time.Duration // informational, displayed to users ("come back in ...")

	// Scheduler.
	InProcessScheduler bool   // run jobs on tickers inside the process (dev only)
	JobsInvoker        string // optional: expected service account e-mail of Cloud Scheduler
	JobsAudience       string // optional: expected OIDC audience of scheduler calls
}

// Load reads the configuration from the environment and validates it.
func Load() (*Config, error) {
	c := &Config{
		Mode:        env("APP_MODE", "all"),
		Port:        env("PORT", "8080"),
		Env:         env("APP_ENV", "dev"),
		BaseURL:     strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		DatabaseURL: env("DATABASE_URL", "postgres://gcalm:gcalm@localhost:5432/gcalm?sslmode=disable"),

		AuthMode:       env("AUTH_MODE", "dev"),
		IAPAudience:    env("IAP_AUDIENCE", ""),
		DevDefaultUser: env("DEV_DEFAULT_USER", "jane.doe@example.com"),

		RolesFile:  env("ROLES_FILE", "config/roles.yaml"),
		PolicyFile: env("POLICY_FILE", "config/policy.yaml"),

		GoogleMode:     env("GOOGLE_MODE", "fake"),
		BillingAccount: env("BILLING_ACCOUNT_ID", ""),
		QuotaProject:   env("QUOTA_PROJECT", ""),
		OrderIDs:       splitList(env("GCA_ORDER_IDS", "")),
		FakeStateFile:  env("FAKE_GOOGLE_STATE_FILE", ".dev/fake-google.json"),
		FakeSeats:      envInt("FAKE_GOOGLE_SEATS", 12),
		FakeMaxLatency: envDuration("FAKE_GOOGLE_MAX_LATENCY", 900*time.Millisecond),
		FakeHangRate:   envFloat("FAKE_GOOGLE_HANG_RATE", 0),
		GCAProjectID:   env("GCA_PROJECT_ID", "my-gemini-project"),
		DemoSeed:       envBool("DEMO_SEED", false),

		CredentialsMode:  env("CREDENTIALS_MODE", "fake"),
		CredentialsGroup: env("CREDENTIALS_GROUP", ""),

		DirectoryMode:    env("DIRECTORY_MODE", "simulated"),
		DirectoryURL:     strings.TrimRight(env("DIRECTORY_URL", ""), "/"),
		DirectoryAPIKey:  env("DIRECTORY_API_KEY", ""),
		DirectoryTimeout: envDuration("DIRECTORY_TIMEOUT", 5*time.Second),
		SimFailureRate:   envFloat("DIRECTORY_SIM_FAILURE_RATE", 0),
		SimMaxLatency:    envDuration("DIRECTORY_SIM_MAX_LATENCY", 600*time.Millisecond),

		MailMode:     env("MAIL_MODE", "log"),
		SMTPHost:     env("SMTP_HOST", "smtp-relay.gmail.com"),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPUser:     env("SMTP_USER", ""),
		SMTPPassword: env("SMTP_PASSWORD", ""),
		MailFrom:     env("MAIL_FROM", "gca-portal@example.com"),

		TrackerMode:   env("TRACKER_MODE", "log"),
		JiraURL:       strings.TrimRight(env("JIRA_URL", ""), "/"),
		JiraUser:      env("JIRA_USER", ""),
		JiraToken:     env("JIRA_TOKEN", ""),
		JiraProject:   env("JIRA_PROJECT", ""),
		JiraIssueType: env("JIRA_ISSUE_TYPE", "Task"),
		GitLabURL:     strings.TrimRight(env("GITLAB_URL", "https://gitlab.com"), "/"),
		GitLabToken:   env("GITLAB_TOKEN", ""),
		GitLabProject: env("GITLAB_PROJECT", ""),

		UXTimeout:        envDuration("UX_TIMEOUT", 10*time.Second),
		JobCallTimeout:   envDuration("JOB_CALL_TIMEOUT", 30*time.Second),
		PoolCacheTTL:     envDuration("POOL_CACHE_TTL", 60*time.Second),
		RateLimitPerMin:  envInt("RATE_LIMIT_PER_MIN", 120),
		RateLimitBurst:   envInt("RATE_LIMIT_BURST", 30),
		MetricsRetention: envDuration("METRICS_RETENTION", 10*24*time.Hour),
		BufferRetryEvery: envDuration("BUFFER_RETRY_EVERY", 7*time.Minute),

		InProcessScheduler: envBool("INPROCESS_SCHEDULER", false),
		JobsInvoker:        env("JOBS_INVOKER", ""),
		JobsAudience:       env("JOBS_AUDIENCE", ""),
	}

	tz, err := time.LoadLocation(env("TIMEZONE", "Europe/Paris"))
	if err != nil {
		return nil, fmt.Errorf("TIMEZONE: %w", err)
	}
	c.TimeZone = tz

	match := env("GCA_ORDER_MATCH", `(?i)gemini|code ?assist|cloudaicompanion`)
	if c.OrderMatch, err = regexp.Compile(match); err != nil {
		return nil, fmt.Errorf("GCA_ORDER_MATCH: %w", err)
	}
	return c, c.validate()
}

// validate checks the combinations that would otherwise fail at runtime.
func (c *Config) validate() error {
	var errs []string
	oneOf := func(name, v string, allowed ...string) {
		for _, a := range allowed {
			if v == a {
				return
			}
		}
		errs = append(errs, fmt.Sprintf("%s=%q must be one of %v", name, v, allowed))
	}
	oneOf("APP_MODE", c.Mode, "web", "worker", "all")
	oneOf("APP_ENV", c.Env, "dev", "prod")
	oneOf("AUTH_MODE", c.AuthMode, "iap", "dev")
	oneOf("GOOGLE_MODE", c.GoogleMode, "real", "fake")
	oneOf("CREDENTIALS_MODE", c.CredentialsMode, "group", "none", "fake")
	oneOf("DIRECTORY_MODE", c.DirectoryMode, "http", "simulated")
	oneOf("MAIL_MODE", c.MailMode, "smtp", "log")
	oneOf("TRACKER_MODE", c.TrackerMode, "jira", "gitlab", "log")

	if c.Env == "prod" && c.DemoSeed {
		errs = append(errs, "DEMO_SEED is forbidden when APP_ENV=prod")
	}
	if c.Env == "prod" && c.AuthMode == "dev" {
		errs = append(errs, "AUTH_MODE=dev is forbidden when APP_ENV=prod")
	}
	if c.AuthMode == "iap" && c.IAPAudience == "" {
		errs = append(errs, "IAP_AUDIENCE is required when AUTH_MODE=iap")
	}
	if c.GoogleMode == "real" && c.BillingAccount == "" {
		errs = append(errs, "BILLING_ACCOUNT_ID is required when GOOGLE_MODE=real")
	}
	if c.CredentialsMode == "group" && c.CredentialsGroup == "" {
		errs = append(errs, "CREDENTIALS_GROUP is required when CREDENTIALS_MODE=group")
	}
	if c.DirectoryMode == "http" && c.DirectoryURL == "" {
		errs = append(errs, "DIRECTORY_URL is required when DIRECTORY_MODE=http")
	}
	if c.TrackerMode == "jira" && (c.JiraURL == "" || c.JiraToken == "" || c.JiraProject == "") {
		errs = append(errs, "JIRA_URL, JIRA_TOKEN and JIRA_PROJECT are required when TRACKER_MODE=jira")
	}
	if c.TrackerMode == "gitlab" && (c.GitLabToken == "" || c.GitLabProject == "") {
		errs = append(errs, "GITLAB_TOKEN and GITLAB_PROJECT are required when TRACKER_MODE=gitlab")
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

// IsDev reports whether developer conveniences are enabled.
func (c *Config) IsDev() bool { return c.Env == "dev" }

// ServesWeb reports whether the portal routes must be mounted.
func (c *Config) ServesWeb() bool { return c.Mode == "web" || c.Mode == "all" }

// ServesJobs reports whether the job routes must be mounted.
func (c *Config) ServesJobs() bool { return c.Mode == "worker" || c.Mode == "all" }

// --- helpers ---------------------------------------------------------------

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(env(key, "")); err == nil {
		return v
	}
	return def
}

func envFloat(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(env(key, ""), 64); err == nil {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	if v, err := strconv.ParseBool(env(key, "")); err == nil {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(env(key, "")); err == nil {
		return v
	}
	return def
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
