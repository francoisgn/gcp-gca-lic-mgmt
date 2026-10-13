package store

import (
	"context"
	"time"
)

// =============================================================================
// API calls (technical KPIs)
// =============================================================================

// InsertAPICall records one outbound call.
func (q Q) InsertAPICall(ctx context.Context, c APICall) error {
	_, err := q.db.Exec(ctx, `INSERT INTO api_calls (ts, target, operation, context, duration_ms, outcome, detail)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, c.TS, c.Target, c.Operation, c.Context, c.DurationMS, c.Outcome, c.Detail)
	return err
}

// PurgeAPICalls deletes calls older than the retention window.
func (q Q) PurgeAPICalls(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM api_calls WHERE ts < $1`, before)
	return tag.RowsAffected(), err
}

// =============================================================================
// Daily stats
// =============================================================================

// IncPageViews increments the page view counter of a day.
func (q Q) IncPageViews(ctx context.Context, day time.Time) error {
	_, err := q.db.Exec(ctx, `INSERT INTO daily_stats (day, page_views) VALUES ($1::date, 1)
		ON CONFLICT (day) DO UPDATE SET page_views = daily_stats.page_views + 1`, day.Format("2006-01-02"))
	return err
}

// UpsertSeatStats stores the seat counters of a day (last value of the day wins).
func (q Q) UpsertSeatStats(ctx context.Context, day time.Time, total, assignedGoogle int) error {
	_, err := q.db.Exec(ctx, `INSERT INTO daily_stats (day, seats_total, seats_assigned_google) VALUES ($1::date,$2,$3)
		ON CONFLICT (day) DO UPDATE SET seats_total=EXCLUDED.seats_total, seats_assigned_google=EXCLUDED.seats_assigned_google`,
		day.Format("2006-01-02"), total, assignedGoogle)
	return err
}

// =============================================================================
// Feedback
// =============================================================================

// InsertFeedback stores a feedback message and fills its id.
func (q Q) InsertFeedback(ctx context.Context, f *Feedback) error {
	return q.db.QueryRow(ctx, `INSERT INTO feedback (user_email, category, message) VALUES ($1,$2,$3)
		RETURNING id, created_at`, f.UserEmail, f.Category, f.Message).Scan(&f.ID, &f.CreatedAt)
}

// SetFeedbackTracker records the issue created in the tracker (or the error).
func (q Q) SetFeedbackTracker(ctx context.Context, id int64, key, url, trackerErr string) error {
	_, err := q.db.Exec(ctx, `UPDATE feedback SET tracker_key=NULLIF($2,''), tracker_url=NULLIF($3,''),
		tracker_error=NULLIF($4,'') WHERE id=$1`, id, key, url, trackerErr)
	return err
}

// ListFeedback returns the feedback of a user ("" = everyone), most recent first.
func (q Q) ListFeedback(ctx context.Context, email string, limit int) ([]Feedback, error) {
	return many[Feedback](ctx, q.db, `SELECT * FROM feedback WHERE ($1='' OR user_email=$1)
		ORDER BY created_at DESC LIMIT $2`, email, limit)
}

// =============================================================================
// Audit log
// =============================================================================

// Audit records an administrator action.
func (q Q) Audit(ctx context.Context, actor, action, target string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	_, err := q.db.Exec(ctx, `INSERT INTO audit_log (actor, action, target, details) VALUES ($1,$2,$3,$4)`,
		actor, action, target, details)
	return err
}

// ListAudit returns the latest audit entries.
func (q Q) ListAudit(ctx context.Context, limit int) ([]AuditEntry, error) {
	return many[AuditEntry](ctx, q.db, `SELECT * FROM audit_log ORDER BY ts DESC LIMIT $1`, limit)
}

// =============================================================================
// Job runs
// =============================================================================

// StartJobRun records the start of a job execution.
func (q Q) StartJobRun(ctx context.Context, job, trigger, actor string) (int64, error) {
	return scalar[int64](ctx, q.db, `INSERT INTO job_runs (job, trigger, actor) VALUES ($1,$2,NULLIF($3,'')) RETURNING id`,
		job, trigger, actor)
}

// FinishJobRun records the end of a job execution.
func (q Q) FinishJobRun(ctx context.Context, id int64, status, summary, errMsg string) error {
	_, err := q.db.Exec(ctx, `UPDATE job_runs SET finished_at=now(), status=$2, summary=NULLIF($3,''), error=NULLIF($4,'')
		WHERE id=$1`, id, status, summary, errMsg)
	return err
}

// LastJobRuns returns the latest execution of each job.
func (q Q) LastJobRuns(ctx context.Context) ([]JobRun, error) {
	return many[JobRun](ctx, q.db, `SELECT DISTINCT ON (job) * FROM job_runs ORDER BY job, started_at DESC`)
}

// LastSuccessfulRun returns the latest successful run of a job.
func (q Q) LastSuccessfulRun(ctx context.Context, job string) (*JobRun, error) {
	return one[JobRun](ctx, q.db, `SELECT * FROM job_runs WHERE job=$1 AND status='OK' ORDER BY started_at DESC LIMIT 1`, job)
}

// PurgeJobRuns deletes job runs older than a date.
func (q Q) PurgeJobRuns(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM job_runs WHERE started_at < $1`, before)
	return tag.RowsAffected(), err
}
