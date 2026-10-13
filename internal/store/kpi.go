package store

import (
	"context"
	"time"
)

// Every KPI is computed per calendar day in the configured time zone. The
// "days" CTE generates the last N days (today included); each metric is
// evaluated at the end of the day, so today's value is the live value.
const daysCTE = `days AS (
	SELECT d::date AS day, ((d::date + 1)::timestamp AT TIME ZONE $1) AS day_end
	FROM generate_series((now() AT TIME ZONE $1)::date - ($2::int - 1), (now() AT TIME ZONE $1)::date, interval '1 day') d)`

// FlowPoint is one day of the license flow (waterfall) chart.
type FlowPoint struct {
	Day             time.Time `db:"day" json:"day"`
	AssignAuto      int       `db:"assign_auto" json:"assignAuto"`
	AssignAdmin     int       `db:"assign_admin" json:"assignAdmin"`
	AssignOther     int       `db:"assign_other" json:"assignOther"`
	UnassignUnused  int       `db:"unassign_unused" json:"unassignUnused"`
	UnassignExpired int       `db:"unassign_expired" json:"unassignExpired"`
	UnassignAdmin   int       `db:"unassign_admin" json:"unassignAdmin"`
	UnassignOther   int       `db:"unassign_other" json:"unassignOther"`
	AssignedEnd     int       `db:"assigned_end" json:"assignedEnd"`
	SeatsTotal      *int      `db:"seats_total" json:"seatsTotal"`
}

// LicenseFlows returns the assignment / unassignment flows per day.
func (q Q) LicenseFlows(ctx context.Context, tz string, days int) ([]FlowPoint, error) {
	return many[FlowPoint](ctx, q.db, `WITH `+daysCTE+`,
	a AS (SELECT (assigned_at AT TIME ZONE $1)::date AS day, source, count(*)::int AS n
	      FROM license_assignments GROUP BY 1, 2),
	e AS (SELECT (ended_at AT TIME ZONE $1)::date AS day, end_reason, count(*)::int AS n
	      FROM license_assignments WHERE ended_at IS NOT NULL GROUP BY 1, 2)
	SELECT d.day,
	  COALESCE((SELECT sum(n) FROM a WHERE a.day=d.day AND source='AUTO'), 0)::int AS assign_auto,
	  COALESCE((SELECT sum(n) FROM a WHERE a.day=d.day AND source='ADMIN'), 0)::int AS assign_admin,
	  COALESCE((SELECT sum(n) FROM a WHERE a.day=d.day AND source NOT IN ('AUTO','ADMIN')), 0)::int AS assign_other,
	  COALESCE((SELECT sum(n) FROM e WHERE e.day=d.day AND end_reason='UNUSED'), 0)::int AS unassign_unused,
	  COALESCE((SELECT sum(n) FROM e WHERE e.day=d.day AND end_reason='EXPIRED'), 0)::int AS unassign_expired,
	  COALESCE((SELECT sum(n) FROM e WHERE e.day=d.day AND end_reason='ADMIN_REMOVAL'), 0)::int AS unassign_admin,
	  COALESCE((SELECT sum(n) FROM e WHERE e.day=d.day AND end_reason NOT IN ('UNUSED','EXPIRED','ADMIN_REMOVAL')), 0)::int AS unassign_other,
	  (SELECT count(*) FROM license_assignments x
	     WHERE x.assigned_at < d.day_end AND (x.ended_at IS NULL OR x.ended_at >= d.day_end))::int AS assigned_end,
	  (SELECT s.seats_total FROM daily_stats s WHERE s.day <= d.day AND s.seats_total IS NOT NULL
	     ORDER BY s.day DESC LIMIT 1) AS seats_total
	FROM days d ORDER BY d.day`, tz, days)
}

// DepthPoint is one day of the queue / buffer / deviation depth chart.
type DepthPoint struct {
	Day        time.Time `db:"day" json:"day"`
	Queue      int       `db:"queue" json:"queue"`
	Buffer     int       `db:"buffer" json:"buffer"`
	Deviations int       `db:"deviations" json:"deviations"`
}

// Depths returns the end-of-day depth of the queue, the buffer and the number
// of open deviations.
func (q Q) Depths(ctx context.Context, tz string, days int) ([]DepthPoint, error) {
	return many[DepthPoint](ctx, q.db, `WITH `+daysCTE+`
	SELECT d.day,
	  (SELECT count(*) FROM queue_entries x WHERE x.enqueued_at < d.day_end
	     AND (x.left_at IS NULL OR x.left_at >= d.day_end))::int AS queue,
	  (SELECT count(*) FROM buffer_entries x WHERE x.created_at < d.day_end
	     AND (x.resolved_at IS NULL OR x.resolved_at >= d.day_end))::int AS buffer,
	  (SELECT count(*) FROM deviations x WHERE x.detected_at < d.day_end
	     AND (x.resolved_at IS NULL OR x.resolved_at >= d.day_end))::int AS deviations
	FROM days d ORDER BY d.day`, tz, days)
}

// OutcomeCount is a number of calls per day and outcome.
type OutcomeCount struct {
	Day     time.Time `db:"day" json:"day"`
	Outcome string    `db:"outcome" json:"outcome"`
	Context string    `db:"context" json:"context"`
	N       int       `db:"n" json:"n"`
}

// CallOutcomes returns the number of calls per day / outcome / context for a target.
func (q Q) CallOutcomes(ctx context.Context, tz string, days int, target string) ([]OutcomeCount, error) {
	return many[OutcomeCount](ctx, q.db, `SELECT (ts AT TIME ZONE $1)::date AS day, outcome, context, count(*)::int AS n
		FROM api_calls WHERE ts >= now() - make_interval(days => $2) AND ($3 = '' OR target = $3)
		GROUP BY 1, 2, 3 ORDER BY 1`, tz, days, target)
}

// LatencyPoint holds response time percentiles for a day / target / operation.
type LatencyPoint struct {
	Day       time.Time `db:"day" json:"day"`
	Target    string    `db:"target" json:"target"`
	Operation string    `db:"operation" json:"operation"`
	N         int       `db:"n" json:"n"`
	P50       float64   `db:"p50" json:"p50"`
	P95       float64   `db:"p95" json:"p95"`
	Max       int       `db:"max" json:"max"`
}

// Latencies returns daily response time percentiles per target / operation,
// computed on successful calls (timeouts have their own KPI).
func (q Q) Latencies(ctx context.Context, tz string, days int) ([]LatencyPoint, error) {
	return many[LatencyPoint](ctx, q.db, `SELECT (ts AT TIME ZONE $1)::date AS day, target, operation, count(*)::int AS n,
		percentile_cont(0.5) WITHIN GROUP (ORDER BY duration_ms) AS p50,
		percentile_cont(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95,
		max(duration_ms) AS max
		FROM api_calls WHERE ts >= now() - make_interval(days => $2) AND outcome = 'OK'
		GROUP BY 1, 2, 3 ORDER BY 1`, tz, days)
}

// AccessPoint compares page views with Google license checks per day.
type AccessPoint struct {
	Day          time.Time `db:"day" json:"day"`
	PageViews    int       `db:"page_views" json:"pageViews"`
	GoogleChecks int       `db:"google_checks" json:"googleChecks"`
}

// Accesses returns page views and Google license checks per day.
func (q Q) Accesses(ctx context.Context, tz string, days int) ([]AccessPoint, error) {
	return many[AccessPoint](ctx, q.db, `WITH `+daysCTE+`
	SELECT d.day,
	  COALESCE((SELECT page_views FROM daily_stats s WHERE s.day=d.day), 0) AS page_views,
	  (SELECT count(*) FROM api_calls c WHERE (c.ts AT TIME ZONE $1)::date = d.day
	     AND c.target='GOOGLE_LICENSE' AND c.operation IN ('enumerate','getPool'))::int AS google_checks
	FROM days d ORDER BY d.day`, tz, days)
}
