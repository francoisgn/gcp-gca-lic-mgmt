// Package jobs runs the scheduled tasks of the portal.
//
// Production: Cloud Scheduler calls POST /jobs/{name} on the "worker" Cloud
// Run service with an OIDC token (Cloud Run IAM authentication: only the
// scheduler service account holds roles/run.invoker). Every run:
//   - takes a PostgreSQL advisory lock dedicated to the job (no overlap even
//     with several instances or a manual run in progress);
//   - is recorded in job_runs (status, duration, summary) for the
//     observability page.
//
// Development: INPROCESS_SCHEDULER=true runs the same jobs on tickers.
package jobs

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"google.golang.org/api/idtoken"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Job is a scheduled task.
type Job struct {
	Name        string
	Description string
	// Cron is the production schedule (documentation + Terraform).
	Cron string
	// Every is the in-process interval used in development.
	Every   time.Duration
	Timeout time.Duration
	Run     func(ctx context.Context) (string, error)
}

// Triggers of a job run.
const (
	TriggerScheduler = "SCHEDULER"
	TriggerManual    = "MANUAL"
	TriggerInProcess = "INPROCESS"
	TriggerEvent     = "EVENT"
)

// Result of a run.
type Result struct {
	Job     string `json:"job"`
	Status  string `json:"status"` // OK | ERROR | SKIPPED
	Summary string `json:"summary"`
	Error   string `json:"error,omitempty"`
}

// Runner executes jobs.
type Runner struct {
	db   *store.DB
	jobs map[string]Job
}

// NewRunner builds a runner.
func NewRunner(db *store.DB, jobs ...Job) *Runner {
	r := &Runner{db: db, jobs: map[string]Job{}}
	for _, j := range jobs {
		r.jobs[j.Name] = j
	}
	return r
}

// Jobs returns the job definitions sorted by name.
func (r *Runner) Jobs() []Job {
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		out = append(out, j)
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name < out[k].Name })
	return out
}

// Run executes a job if it is not already running.
func (r *Runner) Run(ctx context.Context, name, trigger, actor string) (Result, error) {
	j, ok := r.jobs[name]
	if !ok {
		return Result{}, fmt.Errorf("unknown job %q", name)
	}
	res := Result{Job: name}
	ctx = telemetry.WithKind(ctx, telemetry.KindJob)
	ctx, cancel := context.WithTimeout(ctx, j.Timeout)
	defer cancel()
	ran, err := r.db.TryWithLock(ctx, store.JobLockKey(name), func() error {
		id, err := r.db.StartJobRun(ctx, name, trigger, actor)
		if err != nil {
			return err
		}
		start := time.Now()
		summary, runErr := j.Run(ctx)
		res.Summary = summary
		res.Status = "OK"
		if runErr != nil {
			res.Status, res.Error = "ERROR", runErr.Error()
		}
		fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer fcancel()
		if err := r.db.FinishJobRun(fctx, id, res.Status, summary, res.Error); err != nil {
			slog.Error("record job run", "job", name, "err", err)
		}
		slog.Info("job finished", "job", name, "trigger", trigger, "status", res.Status,
			"duration_ms", time.Since(start).Milliseconds(), "summary", summary, "err", res.Error)
		return nil
	})
	if err != nil {
		return res, err
	}
	if !ran {
		res.Status, res.Summary = "SKIPPED", "already running"
	}
	return res, nil
}

// Handler serves POST /jobs/{name} for Cloud Scheduler. When audience is
// set, the OIDC token is verified again in-process (defense in depth on top
// of Cloud Run IAM) and its e-mail must match invoker.
func (r *Runner) Handler(audience, invoker string) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /jobs/{name}", func(w http.ResponseWriter, req *http.Request) {
		if audience != "" {
			if err := verifyScheduler(req, audience, invoker); err != nil {
				slog.Warn("job call rejected", "err", err)
				http.Error(w, "forbidden", http.StatusForbidden)
				return
			}
		}
		res, err := r.Run(req.Context(), req.PathValue("name"), TriggerScheduler, "")
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		if res.Status == "ERROR" {
			// Non-2xx so that Cloud Scheduler records the failure and retries.
			w.WriteHeader(http.StatusInternalServerError)
		}
		_ = json.NewEncoder(w).Encode(res)
	})
	return mux
}

func verifyScheduler(req *http.Request, audience, invoker string) error {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return fmt.Errorf("missing bearer token")
	}
	p, err := idtoken.Validate(req.Context(), token, audience)
	if err != nil {
		return err
	}
	if email, _ := p.Claims["email"].(string); invoker != "" && !strings.EqualFold(email, invoker) {
		return fmt.Errorf("unexpected invoker %q", email)
	}
	return nil
}

// StartInProcess runs every job on its development interval until ctx ends.
func (r *Runner) StartInProcess(ctx context.Context) {
	for _, j := range r.jobs {
		if j.Every <= 0 {
			continue
		}
		go func() {
			t := time.NewTicker(j.Every)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if _, err := r.Run(ctx, j.Name, TriggerInProcess, ""); err != nil {
						slog.Error("in-process job", "job", j.Name, "err", err)
					}
				}
			}
		}()
	}
}
