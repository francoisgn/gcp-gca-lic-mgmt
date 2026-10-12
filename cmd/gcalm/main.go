// Command gcalm runs the Gemini Code Assist license portal.
//
//	gcalm            serve (APP_MODE=web | worker | all)
//	gcalm migrate    apply database migrations and exit
//
// The same image is deployed twice on Cloud Run: "web" (behind IAP) and
// "worker" (scheduled jobs, invoked by Cloud Scheduler).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/jobs"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/mail"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/policy"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/tracker"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/web"
)

// version is set at build time: -ldflags "-X main.version=1.2.3".
var version = "dev"

func main() {
	slog.SetDefault(slog.New(cloudLoggingHandler()))
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		slog.Info("migrations applied")
		return nil
	}

	rl, err := roles.Load(cfg.RolesFile)
	if err != nil {
		return err
	}
	pol, err := policy.Load(cfg.PolicyFile)
	if err != nil {
		return err
	}
	rec := telemetry.NewRecorder(db.Q)

	deps := service.Deps{Cfg: cfg, DB: db, Roles: rl, Policy: pol, Recorder: rec}
	if err := wireIntegrations(ctx, cfg, rec, &deps); err != nil {
		return err
	}
	if deps.Mails, err = mail.NewRenderer(cfg.TimeZone); err != nil {
		return err
	}
	svc := service.New(deps)

	if cfg.DemoSeed {
		if err := svc.SeedDemo(ctx); err != nil {
			return fmt.Errorf("demo seed: %w", err)
		}
	}

	runner := jobs.NewRunner(db, jobList(svc)...)
	// Seats released by an admin action or a purge are served right away;
	// the scheduled queue job is the safety net.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-svc.QueueKicks():
				if _, err := runner.Run(context.WithoutCancel(ctx), "queue", jobs.TriggerEvent, ""); err != nil {
					slog.Error("queue kick", "err", err)
				}
			}
		}
	}()
	if cfg.InProcessScheduler {
		runner.StartInProcess(ctx)
	}

	var authn auth.Authenticator = auth.Dev{DefaultUser: cfg.DevDefaultUser}
	if cfg.AuthMode == "iap" {
		if authn, err = auth.NewIAP(ctx, cfg.IAPAudience); err != nil {
			return err
		}
	}
	srv, err := web.New(cfg, svc, runner, rl, authn, version)
	if err != nil {
		return err
	}

	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      5 * time.Minute, // manual jobs run synchronously
		IdleTimeout:       120 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("listening", "port", cfg.Port, "mode", cfg.Mode, "env", cfg.Env, "google", cfg.GoogleMode,
			"directory", cfg.DirectoryMode, "auth", cfg.AuthMode, "version", version)
		errCh <- httpServer.ListenAndServe()
	}()
	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return httpServer.Shutdown(sctx)
	}
	return nil
}

// wireIntegrations selects the real or simulated adapters and wraps them
// with telemetry.
func wireIntegrations(ctx context.Context, cfg *config.Config, rec *telemetry.Recorder, d *service.Deps) error {
	// Google licenses.
	switch cfg.GoogleMode {
	case "real":
		p, err := gcp.NewProcurement(ctx, cfg.BillingAccount, cfg.QuotaProject, cfg.OrderIDs)
		if err != nil {
			return err
		}
		d.Licensing = gcp.InstrumentLicensing(p, rec)
	default:
		f, err := gcp.NewFakeLicensing(cfg.FakeStateFile, cfg.FakeSeats, cfg.FakeMaxLatency, cfg.FakeHangRate)
		if err != nil {
			return err
		}
		d.Fake = f
		d.Licensing = gcp.InstrumentLicensing(f, rec)
	}

	// Cloud credentials (IAM bundle).
	var cred gcp.Credentials
	switch cfg.CredentialsMode {
	case "group":
		g, err := gcp.NewGroupCredentials(ctx, cfg.CredentialsGroup)
		if err != nil {
			return err
		}
		cred = g
	case "none":
		cred = gcp.NoCredentials{}
	default:
		cred = &gcp.FakeCredentials{MaxLatency: cfg.FakeMaxLatency / 2}
	}
	d.Credentials = gcp.InstrumentCredentials(cred, rec)

	// Enterprise directory.
	var dir directory.Client = &directory.Simulator{MaxLatency: cfg.SimMaxLatency, FailureRate: cfg.SimFailureRate}
	if cfg.DirectoryMode == "http" {
		dir = directory.NewHTTPClient(cfg.DirectoryURL, cfg.DirectoryAPIKey)
	}
	d.Directory = directory.Instrument(dir, rec)

	// Mail.
	if cfg.MailMode == "smtp" {
		d.Mailer = mail.SMTPMailer{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Password: cfg.SMTPPassword, From: cfg.MailFrom}
	} else {
		d.Mailer = mail.LogMailer{}
	}

	// Feedback tracker.
	var tr tracker.Tracker = tracker.Log{}
	switch cfg.TrackerMode {
	case "jira":
		tr = tracker.Jira{BaseURL: cfg.JiraURL, User: cfg.JiraUser, Token: cfg.JiraToken, Project: cfg.JiraProject, IssueType: cfg.JiraIssueType}
	case "gitlab":
		tr = tracker.GitLab{BaseURL: cfg.GitLabURL, Token: cfg.GitLabToken, Project: cfg.GitLabProject}
	}
	d.Tracker = tracker.Instrument(tr, rec)
	return nil
}

// jobList declares the scheduled jobs. Cron expressions are the production
// schedules (mirrored in deploy/terraform/scheduler.tf).
func jobList(svc *service.Service) []jobs.Job {
	return []jobs.Job{
		{Name: "buffer-retry", Description: "Retry timed-out requests (buffer) and failed e-mails",
			Cron: "*/7 * * * *", Every: 7 * time.Minute, Timeout: 6 * time.Minute, Run: svc.RetryBuffer},
		{Name: "queue", Description: "Serve the waiting list when seats are free",
			Cron: "3-59/7 * * * *", Every: 7 * time.Minute, Timeout: 6 * time.Minute, Run: svc.ProcessQueue},
		{Name: "subscription-refresh", Description: "Detect subscription changes, migrate licenses, reconcile with Google",
			Cron: "0 */3 * * *", Every: 3 * time.Hour, Timeout: 15 * time.Minute, Run: svc.RefreshSubscriptions},
		{Name: "purge", Description: "Remove expired licenses and licenses unused for 7 days",
			Cron: "15 * * * *", Every: time.Hour, Timeout: 15 * time.Minute, Run: svc.Purge},
		{Name: "housekeeping", Description: "Technical KPI retention (10 days) and job history cleanup",
			Cron: "30 3 * * *", Every: 24 * time.Hour, Timeout: 10 * time.Minute, Run: svc.Housekeeping},
	}
}

// cloudLoggingHandler writes JSON logs understood by Cloud Logging
// ("severity" and "message" keys).
func cloudLoggingHandler() slog.Handler {
	level := slog.LevelInfo
	if os.Getenv("LOG_LEVEL") == "debug" {
		level = slog.LevelDebug
	}
	return slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			switch a.Key {
			case slog.LevelKey:
				a.Key = "severity"
				if lvl, ok := a.Value.Any().(slog.Level); ok && lvl == slog.LevelWarn {
					a.Value = slog.StringValue("WARNING")
				}
			case slog.MessageKey:
				a.Key = "message"
			}
			return a
		},
	})
}
