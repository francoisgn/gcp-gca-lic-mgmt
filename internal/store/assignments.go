package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// ActiveAssignment returns the active assignment of a user, or ErrNotFound.
func (q Q) ActiveAssignment(ctx context.Context, email string) (*Assignment, error) {
	return one[Assignment](ctx, q.db, `SELECT * FROM license_assignments WHERE user_email=$1 AND ended_at IS NULL`, email)
}

// ActiveAssignments returns every active assignment of a subscription.
func (q Q) ActiveAssignments(ctx context.Context, subscriptionID string) ([]Assignment, error) {
	return many[Assignment](ctx, q.db, `SELECT * FROM license_assignments
		WHERE subscription_id=$1 AND ended_at IS NULL ORDER BY assigned_at, id`, subscriptionID)
}

// AllActiveAssignments returns every active assignment whatever the subscription.
func (q Q) AllActiveAssignments(ctx context.Context) ([]Assignment, error) {
	return many[Assignment](ctx, q.db, `SELECT * FROM license_assignments WHERE ended_at IS NULL ORDER BY assigned_at, id`)
}

// AssignmentHistory returns the ended assignments of a user, most recent first.
func (q Q) AssignmentHistory(ctx context.Context, email string, limit int) ([]Assignment, error) {
	return many[Assignment](ctx, q.db, `SELECT * FROM license_assignments
		WHERE user_email=$1 AND ended_at IS NOT NULL ORDER BY ended_at DESC LIMIT $2`, email, limit)
}

// InsertAssignment creates an assignment and fills its id.
func (q Q) InsertAssignment(ctx context.Context, a *Assignment) error {
	return q.db.QueryRow(ctx, `INSERT INTO license_assignments
		(user_email, subscription_id, request_id, source, assigned_by, duration, assigned_at, expires_at,
		 last_usage_at, site, business_unit, gps_code)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id`,
		a.UserEmail, a.SubscriptionID, a.RequestID, a.Source, a.AssignedBy, a.Duration, a.AssignedAt, a.ExpiresAt,
		a.LastUsageAt, a.Site, a.BusinessUnit, a.GPSCode).Scan(&a.ID)
}

// EndAssignment closes an assignment with a reason.
func (q Q) EndAssignment(ctx context.Context, id int64, reason domain.EndReason, actor string, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE license_assignments SET ended_at=$2, end_reason=$3, ended_by=NULLIF($4,'')
		WHERE id=$1 AND ended_at IS NULL`, id, at, reason, actor)
	return err
}

// EndActiveAssignmentsByEmail closes the active assignments of the given users.
func (q Q) EndActiveAssignmentsByEmail(ctx context.Context, emails []string, reason domain.EndReason, actor string, at time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE license_assignments SET ended_at=$2, end_reason=$3, ended_by=NULLIF($4,'')
		WHERE user_email = ANY($1) AND ended_at IS NULL`, emails, at, reason, actor)
	return tag.RowsAffected(), err
}

// SetLastUsage records the last usage reported by Google.
func (q Q) SetLastUsage(ctx context.Context, id int64, at *time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE license_assignments SET last_usage_at=$2 WHERE id=$1`, id, at)
	return err
}

// MoveAssignment re-attaches an assignment to a new subscription (rollover).
func (q Q) MoveAssignment(ctx context.Context, id int64, subscriptionID string, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE license_assignments SET subscription_id=$2, migrated_at=$3 WHERE id=$1`, id, subscriptionID, at)
	return err
}

// ExpiredAssignments returns active assignments whose expiry date is passed.
func (q Q) ExpiredAssignments(ctx context.Context, now time.Time) ([]Assignment, error) {
	return many[Assignment](ctx, q.db, `SELECT * FROM license_assignments WHERE ended_at IS NULL AND expires_at <= $1`, now)
}

// CountActiveAssignments returns the number of active assignments.
func (q Q) CountActiveAssignments(ctx context.Context) (int, error) {
	return scalar[int](ctx, q.db, `SELECT count(*) FROM license_assignments WHERE ended_at IS NULL`)
}

// --- Inventory -----------------------------------------------------------------

// InventoryFilter selects active assignments for the inventory and the
// criteria-based mass removal. Empty fields are ignored.
type InventoryFilter struct {
	Search         string   `json:"search"` // substring of the e-mail
	Sites          []string `json:"sites"`
	BusinessUnits  []string `json:"businessUnits"`
	Sources        []string `json:"sources"`
	UnusedDaysMin  int      `json:"unusedDaysMin"`  // no usage for at least N days
	ExpiresInDays  int      `json:"expiresInDays"`  // expires within N days (>0)
	AssignedBefore string   `json:"assignedBefore"` // YYYY-MM-DD
}

// where builds the SQL predicate and arguments of a filter.
func (f InventoryFilter) where(now time.Time) (string, []any) {
	conds := []string{"ended_at IS NULL"}
	var args []any
	add := func(cond string, v any) {
		args = append(args, v)
		conds = append(conds, strings.ReplaceAll(cond, "?", fmt.Sprintf("$%d", len(args))))
	}
	if s := strings.TrimSpace(f.Search); s != "" {
		add("user_email ILIKE ?", "%"+s+"%")
	}
	if len(f.Sites) > 0 {
		add("site = ANY(?)", f.Sites)
	}
	if len(f.BusinessUnits) > 0 {
		add("business_unit = ANY(?)", f.BusinessUnits)
	}
	if len(f.Sources) > 0 {
		add("source = ANY(?)", f.Sources)
	}
	if f.UnusedDaysMin > 0 {
		add("COALESCE(last_usage_at, assigned_at) <= ?", now.AddDate(0, 0, -f.UnusedDaysMin))
	}
	if f.ExpiresInDays > 0 {
		add("expires_at <= ?", now.AddDate(0, 0, f.ExpiresInDays))
	}
	if t, err := time.Parse("2006-01-02", f.AssignedBefore); err == nil {
		add("assigned_at < ?", t)
	}
	return strings.Join(conds, " AND "), args
}

// Inventory returns the active assignments matching a filter.
func (q Q) Inventory(ctx context.Context, f InventoryFilter, now time.Time, limit int) ([]Assignment, int, error) {
	where, args := f.where(now)
	total, err := scalar[int](ctx, q.db, `SELECT count(*) FROM license_assignments WHERE `+where, args...)
	if err != nil {
		return nil, 0, err
	}
	rows, err := many[Assignment](ctx, q.db, fmt.Sprintf(`SELECT * FROM license_assignments WHERE %s
		ORDER BY assigned_at DESC, id DESC LIMIT %d`, where, limit), args...)
	return rows, total, err
}

// InventoryEmails returns every e-mail matching a filter (mass removal).
func (q Q) InventoryEmails(ctx context.Context, f InventoryFilter, now time.Time) ([]string, error) {
	where, args := f.where(now)
	rows, err := q.db.Query(ctx, `SELECT user_email FROM license_assignments WHERE `+where+` ORDER BY user_email`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Facets are the distinct values used to build the filter chips.
type Facets struct {
	Sites         []string `json:"sites"`
	BusinessUnits []string `json:"businessUnits"`
	Sources       []string `json:"sources"`
}

// InventoryFacets returns the distinct values of the filterable columns.
func (q Q) InventoryFacets(ctx context.Context) (Facets, error) {
	var f Facets
	err := q.db.QueryRow(ctx, `SELECT
		COALESCE(array_agg(DISTINCT site) FILTER (WHERE site IS NOT NULL), '{}'),
		COALESCE(array_agg(DISTINCT business_unit) FILTER (WHERE business_unit IS NOT NULL), '{}'),
		COALESCE(array_agg(DISTINCT source), '{}')
		FROM license_assignments WHERE ended_at IS NULL`).Scan(&f.Sites, &f.BusinessUnits, &f.Sources)
	return f, err
}
