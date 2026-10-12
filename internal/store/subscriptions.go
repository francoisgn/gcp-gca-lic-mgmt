package store

import (
	"context"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// CurrentSubscription returns the subscription used for new assignments, or
// ErrNotFound before the first refresh.
func (q Q) CurrentSubscription(ctx context.Context) (*Subscription, error) {
	return one[Subscription](ctx, q.db, `SELECT * FROM subscriptions WHERE is_current`)
}

// GetSubscription returns a subscription by id.
func (q Q) GetSubscription(ctx context.Context, id string) (*Subscription, error) {
	return one[Subscription](ctx, q.db, `SELECT * FROM subscriptions WHERE id=$1`, id)
}

// ListSubscriptions returns every known subscription, most recent first.
func (q Q) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	return many[Subscription](ctx, q.db, `SELECT * FROM subscriptions ORDER BY start_time DESC NULLS LAST, first_seen_at DESC`)
}

// UpsertSubscription inserts or refreshes a subscription seen in Google.
// is_current and replaced_by are managed by SetCurrentSubscription.
func (q Q) UpsertSubscription(ctx context.Context, s Subscription, now time.Time) error {
	_, err := q.db.Exec(ctx, `INSERT INTO subscriptions
		(id, display_name, offer, state, seats_total, seats_available, start_time, end_time, auto_renew,
		 first_seen_at, last_seen_at, inactive_since)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10, CASE WHEN $4='INACTIVE' THEN $10::timestamptz END)
		ON CONFLICT (id) DO UPDATE SET
		  display_name=EXCLUDED.display_name, offer=EXCLUDED.offer, state=EXCLUDED.state,
		  seats_total=EXCLUDED.seats_total, seats_available=EXCLUDED.seats_available,
		  start_time=EXCLUDED.start_time, end_time=EXCLUDED.end_time, auto_renew=EXCLUDED.auto_renew,
		  last_seen_at=EXCLUDED.last_seen_at,
		  inactive_since=CASE WHEN EXCLUDED.state='INACTIVE' THEN COALESCE(subscriptions.inactive_since, EXCLUDED.last_seen_at) END`,
		s.ID, s.DisplayName, s.Offer, s.State, s.SeatsTotal, s.SeatsAvailable, s.StartTime, s.EndTime, s.AutoRenew, now)
	return err
}

// MarkUnseenInactive marks as inactive the subscriptions not returned by Google anymore.
func (q Q) MarkUnseenInactive(ctx context.Context, seen []string, now time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE subscriptions SET state='INACTIVE', inactive_since=COALESCE(inactive_since,$2)
		WHERE NOT (id = ANY($1)) AND state='ACTIVE'`, seen, now)
	return err
}

// SetCurrentSubscription switches the current subscription. When previous is
// not empty, it records which subscription replaced it.
func (q Q) SetCurrentSubscription(ctx context.Context, id, previous string) error {
	if _, err := q.db.Exec(ctx, `UPDATE subscriptions SET is_current=FALSE WHERE is_current AND id<>$1`, id); err != nil {
		return err
	}
	if previous != "" && previous != id {
		if _, err := q.db.Exec(ctx, `UPDATE subscriptions SET replaced_by=$2 WHERE id=$1`, previous, id); err != nil {
			return err
		}
	}
	_, err := q.db.Exec(ctx, `UPDATE subscriptions SET is_current=TRUE WHERE id=$1`, id)
	return err
}

// UpdateSeats stores the latest pool counters.
func (q Q) UpdateSeats(ctx context.Context, id string, total, available int) error {
	_, err := q.db.Exec(ctx, `UPDATE subscriptions SET seats_total=$2, seats_available=$3 WHERE id=$1`, id, total, available)
	return err
}

// =============================================================================
// Deviations
// =============================================================================

// UpsertDeviation opens a deviation (or refreshes last_seen_at if already open).
func (q Q) UpsertDeviation(ctx context.Context, email, subscriptionID string, kind domain.DeviationKind, now time.Time) error {
	_, err := q.db.Exec(ctx, `INSERT INTO deviations (user_email, subscription_id, kind, detected_at, last_seen_at)
		VALUES ($1,$2,$3,$4,$4)
		ON CONFLICT (user_email, subscription_id, kind) WHERE resolved_at IS NULL DO UPDATE SET last_seen_at=EXCLUDED.last_seen_at`,
		email, subscriptionID, kind, now)
	return err
}

// SelfHealDeviations closes the open deviations of a subscription that were
// not observed during the latest reconciliation.
func (q Q) SelfHealDeviations(ctx context.Context, subscriptionID string, observedAt time.Time) (int64, error) {
	tag, err := q.db.Exec(ctx, `UPDATE deviations SET resolved_at=$2, resolution='SELF_HEALED'
		WHERE subscription_id=$1 AND resolved_at IS NULL AND last_seen_at < $2`, subscriptionID, observedAt)
	return tag.RowsAffected(), err
}

// OpenDeviations returns the deviations not resolved yet.
func (q Q) OpenDeviations(ctx context.Context) ([]Deviation, error) {
	return many[Deviation](ctx, q.db, `SELECT * FROM deviations WHERE resolved_at IS NULL ORDER BY detected_at, id`)
}

// GetDeviation returns a deviation by id.
func (q Q) GetDeviation(ctx context.Context, id int64) (*Deviation, error) {
	return one[Deviation](ctx, q.db, `SELECT * FROM deviations WHERE id=$1`, id)
}

// ResolveDeviation closes a deviation with an explicit resolution.
func (q Q) ResolveDeviation(ctx context.Context, id int64, res domain.DeviationResolution, actor string, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE deviations SET resolved_at=$2, resolution=$3, resolved_by=$4
		WHERE id=$1 AND resolved_at IS NULL`, id, at, res, actor)
	return err
}
