package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// openStatuses is the SQL list of open request statuses.
const openStatuses = `('DRAFT','DIRECTORY_KO','PENDING_ADMIN','QUEUED','BUFFERED')`

// GetRequest returns a request by id.
func (q Q) GetRequest(ctx context.Context, id int64) (*Request, error) {
	return one[Request](ctx, q.db, `SELECT * FROM license_requests WHERE id=$1`, id)
}

// GetRequestForUpdate returns a request and locks its row until the end of
// the transaction.
func (q Q) GetRequestForUpdate(ctx context.Context, id int64) (*Request, error) {
	return one[Request](ctx, q.db, `SELECT * FROM license_requests WHERE id=$1 FOR UPDATE`, id)
}

// OpenRequestFor returns the open request of a user, or ErrNotFound.
func (q Q) OpenRequestFor(ctx context.Context, email string) (*Request, error) {
	return one[Request](ctx, q.db, `SELECT * FROM license_requests
		WHERE user_email=$1 AND status IN `+openStatuses+` ORDER BY id DESC LIMIT 1`, email)
}

// LatestRequestFor returns the most recent request of a user, or ErrNotFound.
func (q Q) LatestRequestFor(ctx context.Context, email string) (*Request, error) {
	return one[Request](ctx, q.db, `SELECT * FROM license_requests
		WHERE user_email=$1 AND status <> 'DRAFT' ORDER BY id DESC LIMIT 1`, email)
}

// CreateRequest inserts a request and fills its id and timestamps.
func (q Q) CreateRequest(ctx context.Context, r *Request) error {
	if r.Answers == nil {
		r.Answers = map[string]string{}
	}
	if r.Origin == "" {
		r.Origin = "FORM"
	}
	return q.db.QueryRow(ctx, `INSERT INTO license_requests (user_email, status, origin, training_ack, duration, answers)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING id, created_at, updated_at`,
		r.UserEmail, r.Status, r.Origin, r.TrainingAck, r.Duration, r.Answers,
	).Scan(&r.ID, &r.CreatedAt, &r.UpdatedAt)
}

// UpdateRequest persists every mutable column of a request.
func (q Q) UpdateRequest(ctx context.Context, r *Request) error {
	r.UpdatedAt = time.Now()
	_, err := q.db.Exec(ctx, `UPDATE license_requests SET
		status=@status, updated_at=@updated_at, training_ack=@training_ack, duration=@duration, answers=@answers,
		dir_outcome=@dir_outcome, dir_checked_at=@dir_checked_at, dir_account_status=@dir_account_status,
		dir_site=@dir_site, dir_business_unit=@dir_business_unit, dir_gps_code=@dir_gps_code, dir_reason=@dir_reason,
		escalated_at=@escalated_at, esc_division=@esc_division, esc_entity=@esc_entity, esc_project=@esc_project,
		esc_comment=@esc_comment, decision=@decision, decided_at=@decided_at, decided_by=@decided_by,
		decision_reason=@decision_reason, reserved_at=@reserved_at, credentials_at=@credentials_at, license_at=@license_at, email_at=@email_at,
		queued_at=@queued_at, buffered_at=@buffered_at, completed_at=@completed_at, failure_reason=@failure_reason,
		assignment_id=@assignment_id
		WHERE id=@id`, pgx.NamedArgs{
		"id": r.ID, "status": r.Status, "updated_at": r.UpdatedAt, "training_ack": r.TrainingAck,
		"duration": r.Duration, "answers": r.Answers,
		"dir_outcome": r.DirOutcome, "dir_checked_at": r.DirCheckedAt, "dir_account_status": r.DirAccountStatus,
		"dir_site": r.DirSite, "dir_business_unit": r.DirBusinessUnit, "dir_gps_code": r.DirGPSCode, "dir_reason": r.DirReason,
		"escalated_at": r.EscalatedAt, "esc_division": r.EscDivision, "esc_entity": r.EscEntity, "esc_project": r.EscProject,
		"esc_comment": r.EscComment, "decision": r.Decision, "decided_at": r.DecidedAt, "decided_by": r.DecidedBy,
		"decision_reason": r.DecisionReason, "reserved_at": r.ReservedAt, "credentials_at": r.CredentialsAt, "license_at": r.LicenseAt, "email_at": r.EmailAt,
		"queued_at": r.QueuedAt, "buffered_at": r.BufferedAt, "completed_at": r.CompletedAt, "failure_reason": r.FailureReason,
		"assignment_id": r.AssignmentID,
	})
	return err
}

// RequestCounts returns the number of requests (drafts excluded) and the
// number of rejected requests of a user.
func (q Q) RequestCounts(ctx context.Context, email string) (total, rejected int, err error) {
	err = q.db.QueryRow(ctx, `SELECT count(*) FILTER (WHERE status <> 'DRAFT'),
		count(*) FILTER (WHERE status = 'REJECTED') FROM license_requests WHERE user_email=$1`, email).Scan(&total, &rejected)
	return
}

// RejectedRequests returns a page of rejected requests, most recent first.
func (q Q) RejectedRequests(ctx context.Context, email string, limit, offset int) ([]Request, error) {
	return many[Request](ctx, q.db, `SELECT * FROM license_requests WHERE user_email=$1 AND status='REJECTED'
		ORDER BY decided_at DESC NULLS LAST, id DESC LIMIT $2 OFFSET $3`, email, limit, offset)
}

// PendingAdminRequests returns the escalations waiting for a decision (oldest first).
func (q Q) PendingAdminRequests(ctx context.Context) ([]Request, error) {
	return many[Request](ctx, q.db, `SELECT * FROM license_requests WHERE status='PENDING_ADMIN' ORDER BY escalated_at, id`)
}

// RecentDecisions returns the last administrator decisions.
func (q Q) RecentDecisions(ctx context.Context, limit int) ([]Request, error) {
	return many[Request](ctx, q.db, `SELECT * FROM license_requests WHERE decided_at IS NOT NULL
		ORDER BY decided_at DESC LIMIT $1`, limit)
}

// AssignedWithoutEmail returns assigned requests whose confirmation e-mail
// could not be sent yet (retried by the buffer job).
func (q Q) AssignedWithoutEmail(ctx context.Context, since time.Time) ([]Request, error) {
	return many[Request](ctx, q.db, `SELECT * FROM license_requests
		WHERE status='ASSIGNED' AND email_at IS NULL AND completed_at > $1 ORDER BY id LIMIT 100`, since)
}

// CountByStatus returns the number of requests in a status.
func (q Q) CountByStatus(ctx context.Context, s domain.RequestStatus) (int, error) {
	return scalar[int](ctx, q.db, `SELECT count(*) FROM license_requests WHERE status=$1`, s)
}

// reservationPredicate selects the requests holding a seat reservation.
// A buffered request keeps its reservation until it is retried; any other
// reservation older than 15 minutes is considered stale (crashed process).
const reservationPredicate = `reserved_at IS NOT NULL AND license_at IS NULL
	AND (status = 'BUFFERED' OR (status IN ('DRAFT','QUEUED','PENDING_ADMIN','DIRECTORY_KO')
	     AND reserved_at > now() - interval '15 minutes'))`

// ReservedSeats returns the number of seats reserved by in-flight or
// buffered requests (they have priority over the queue). excludeRequestID
// lets a request not count its own reservation.
func (q Q) ReservedSeats(ctx context.Context, excludeRequestID int64) (int, error) {
	return scalar[int](ctx, q.db, `SELECT count(*) FROM license_requests WHERE `+reservationPredicate+` AND id <> $1`, excludeRequestID)
}

// ReservedEmails returns the users holding a reservation (excluded from the
// GOOGLE_ONLY deviation detection: their DB record is being written).
func (q Q) ReservedEmails(ctx context.Context) ([]string, error) {
	rows, err := q.db.Query(ctx, `SELECT user_email FROM license_requests WHERE `+reservationPredicate)
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
