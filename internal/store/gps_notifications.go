package store

import (
	"context"
	"strings"
	"time"
)

// =============================================================================
// Allowlist of GPS codes (automatic approval)
// =============================================================================

// IsGPSAllowed reports whether a GPS code is in the allowlist.
func (q Q) IsGPSAllowed(ctx context.Context, code string) (bool, error) {
	return scalar[bool](ctx, q.db, `SELECT EXISTS(SELECT 1 FROM allowed_gps_codes WHERE code = lower($1))`, strings.TrimSpace(code))
}

// ListGPSCodes returns the allowlist with the number of active licenses per code.
func (q Q) ListGPSCodes(ctx context.Context) ([]GPSCode, error) {
	return many[GPSCode](ctx, q.db, `SELECT g.*, (SELECT count(*) FROM license_assignments a
		WHERE a.ended_at IS NULL AND lower(a.gps_code) = g.code)::int AS licenses
		FROM allowed_gps_codes g ORDER BY g.code`)
}

// AddGPSCodes inserts codes (already validated, lower-cased). Existing codes
// are kept unchanged. Returns the number of codes actually added.
func (q Q) AddGPSCodes(ctx context.Context, codes []string, description, actor string) (int64, error) {
	tag, err := q.db.Exec(ctx, `INSERT INTO allowed_gps_codes (code, description, added_by)
		SELECT c, $2, $3 FROM unnest($1::text[]) AS c ON CONFLICT (code) DO NOTHING`, codes, description, actor)
	return tag.RowsAffected(), err
}

// RemoveGPSCode deletes a code from the allowlist.
func (q Q) RemoveGPSCode(ctx context.Context, code string) (bool, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM allowed_gps_codes WHERE code = lower($1)`, code)
	return tag.RowsAffected() > 0, err
}

// =============================================================================
// Monthly directory re-check
// =============================================================================

// DueForDirectoryCheck returns the active licenses to re-check tonight:
//   - on the monthly anniversary of the assignment (day of month; licenses
//     assigned on the 29th–31st are checked on the last day of shorter months);
//   - or not checked successfully for 35 days (catch-up after errors / misses);
//   - never twice within 20 days, never during the first 20 days.
func (q Q) DueForDirectoryCheck(ctx context.Context, tz string, limit int) ([]Assignment, error) {
	return many[Assignment](ctx, q.db, `
	WITH p AS (SELECT (now() AT TIME ZONE $1)::date AS today)
	SELECT a.* FROM license_assignments a, p
	WHERE a.ended_at IS NULL
	  AND a.assigned_at < now() - interval '20 days'
	  AND (a.dir_checked_at IS NULL OR a.dir_checked_at < now() - interval '20 days')
	  AND (
	        extract(day FROM (a.assigned_at AT TIME ZONE $1)) = extract(day FROM p.today)
	     OR (extract(day FROM (a.assigned_at AT TIME ZONE $1)) > extract(day FROM p.today)
	         AND p.today = (date_trunc('month', p.today) + interval '1 month - 1 day')::date)
	     OR coalesce(a.dir_checked_at, a.assigned_at) < now() - interval '35 days')
	ORDER BY coalesce(a.dir_checked_at, a.assigned_at)
	LIMIT $2`, tz, limit)
}

// DirectoryCheck is the result of one re-check.
type DirectoryCheck struct {
	Result           string // OK | CHANGED | INELIGIBLE
	Status           string
	Site             string
	BusinessUnit     string
	GPSCode          string
	DriftFields      []string
	IneligibleReason string
}

// SetDirectoryCheck records a successful re-check. Missing baseline values
// (licenses assigned by an administrator without directory data) adopt the
// current directory values.
func (q Q) SetDirectoryCheck(ctx context.Context, id int64, c DirectoryCheck, at time.Time) error {
	if c.DriftFields == nil {
		c.DriftFields = []string{}
	}
	_, err := q.db.Exec(ctx, `UPDATE license_assignments SET
		dir_checked_at = $2, dir_check_result = $3, dir_check_error = NULL,
		dir_current_status = $4, dir_current_site = $5, dir_current_business_unit = $6, dir_current_gps_code = $7,
		drift_fields = $8,
		drift_detected_at = CASE WHEN cardinality($8::text[]) = 0 THEN NULL ELSE coalesce(drift_detected_at, $2) END,
		ineligible_reason = NULLIF($9, ''),
		site = coalesce(site, NULLIF($5, '')),
		business_unit = coalesce(business_unit, NULLIF($6, '')),
		gps_code = coalesce(gps_code, NULLIF($7, ''))
		WHERE id = $1`, id, at, c.Result, c.Status, c.Site, c.BusinessUnit, c.GPSCode, c.DriftFields, c.IneligibleReason)
	return err
}

// SetDirectoryCheckError records a failed re-check (retried the next night).
func (q Q) SetDirectoryCheckError(ctx context.Context, id int64, msg string) error {
	_, err := q.db.Exec(ctx, `UPDATE license_assignments SET dir_check_result = 'ERROR', dir_check_error = $2 WHERE id = $1`, id, msg)
	return err
}

// AcknowledgeDrift accepts the new directory values as the baseline of the
// given users' licenses. Ineligibility is not cleared (fix the cause first).
func (q Q) AcknowledgeDrift(ctx context.Context, emails []string) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE license_assignments SET
		site = coalesce(dir_current_site, site), business_unit = coalesce(dir_current_business_unit, business_unit),
		gps_code = coalesce(dir_current_gps_code, gps_code),
		drift_fields = '{}', drift_detected_at = NULL,
		dir_check_result = CASE WHEN ineligible_reason IS NULL THEN 'OK' ELSE 'INELIGIBLE' END
		WHERE user_email = ANY($1) AND ended_at IS NULL AND cardinality(drift_fields) > 0`, emails)
	return tag.RowsAffected(), err
}

// =============================================================================
// Notification journal
// =============================================================================

// InsertNotification records an e-mail notification.
func (q Q) InsertNotification(ctx context.Context, n Notification) error {
	_, err := q.db.Exec(ctx, `INSERT INTO notifications (event, recipient, subject, body_text, status, error, request_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, n.Event, n.Recipient, n.Subject, n.BodyText, n.Status, n.Error, n.RequestID)
	return err
}

// ListNotifications returns the latest notifications ("" = all recipients).
func (q Q) ListNotifications(ctx context.Context, recipient string, limit int) ([]Notification, error) {
	return many[Notification](ctx, q.db, `SELECT * FROM notifications WHERE ($1 = '' OR recipient = $1)
		ORDER BY ts DESC, id DESC LIMIT $2`, recipient, limit)
}

// PurgeNotifications deletes notifications older than a date.
func (q Q) PurgeNotifications(ctx context.Context, before time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `DELETE FROM notifications WHERE ts < $1`, before)
	return tag.RowsAffected(), err
}

// =============================================================================
// License mix KPIs (external accounts, directory drift)
// =============================================================================

// LicenseMix counts active licenses by account type and directory status.
type LicenseMix struct {
	Active       int `json:"active"`
	External     int `json:"external"`
	Changed      int `json:"changed"`
	Ineligible   int `json:"ineligible"`
	CheckErrors  int `json:"checkErrors"`
	NeverChecked int `json:"neverChecked"`
}

// LicenseMixStats returns the license mix. externalSuffix is the local-part
// suffix of external accounts (".ext").
func (q Q) LicenseMixStats(ctx context.Context, externalSuffix string) (LicenseMix, error) {
	var m LicenseMix
	err := q.db.QueryRow(ctx, `SELECT count(*),
		count(*) FILTER (WHERE $1 <> '' AND right(split_part(user_email, '@', 1), length($1)) = $1),
		count(*) FILTER (WHERE dir_check_result = 'CHANGED'),
		count(*) FILTER (WHERE dir_check_result = 'INELIGIBLE'),
		count(*) FILTER (WHERE dir_check_result = 'ERROR'),
		count(*) FILTER (WHERE dir_checked_at IS NULL)
		FROM license_assignments WHERE ended_at IS NULL`, strings.ToLower(externalSuffix)).
		Scan(&m.Active, &m.External, &m.Changed, &m.Ineligible, &m.CheckErrors, &m.NeverChecked)
	return m, err
}
