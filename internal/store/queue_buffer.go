package store

import (
	"context"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// =============================================================================
// Queue (waiting list when the pool is exhausted)
// =============================================================================

// queueOrder is the serving order: priority first, then FIFO.
const queueOrder = `priority DESC, enqueued_at, id`

// Enqueue adds a user to the waiting list. It is idempotent: if the user is
// already waiting, the existing entry is returned.
func (q Q) Enqueue(ctx context.Context, requestID int64, email string, priority int, at time.Time) (*QueueEntry, error) {
	_, err := q.db.Exec(ctx, `INSERT INTO queue_entries (request_id, user_email, priority, enqueued_at)
		VALUES ($1,$2,$3,$4) ON CONFLICT (user_email) WHERE status='WAITING' DO NOTHING`, requestID, email, priority, at)
	if err != nil {
		return nil, err
	}
	return q.WaitingEntry(ctx, email)
}

// WaitingEntry returns the waiting entry of a user with its 1-based position.
func (q Q) WaitingEntry(ctx context.Context, email string) (*QueueEntry, error) {
	return one[QueueEntry](ctx, q.db, `SELECT * FROM (
		SELECT *, row_number() OVER (ORDER BY `+queueOrder+`)::int AS position
		FROM queue_entries WHERE status='WAITING') t WHERE user_email=$1`, email)
}

// QueueDepth returns the number of waiting users.
func (q Q) QueueDepth(ctx context.Context) (int, error) {
	return scalar[int](ctx, q.db, `SELECT count(*) FROM queue_entries WHERE status='WAITING'`)
}

// QueueHead returns the next entry to serve, or ErrNotFound.
func (q Q) QueueHead(ctx context.Context) (*QueueEntry, error) {
	return one[QueueEntry](ctx, q.db, `SELECT *, 1 AS position FROM queue_entries WHERE status='WAITING'
		ORDER BY `+queueOrder+` LIMIT 1`)
}

// ListQueue returns the waiting list in serving order.
func (q Q) ListQueue(ctx context.Context) ([]QueueEntry, error) {
	return many[QueueEntry](ctx, q.db, `SELECT *, row_number() OVER (ORDER BY `+queueOrder+`)::int AS position
		FROM queue_entries WHERE status='WAITING' ORDER BY `+queueOrder)
}

// CloseQueueEntries closes the waiting entry of a request (served, left...).
func (q Q) CloseQueueEntries(ctx context.Context, requestID int64, status domain.QueueStatus, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE queue_entries SET status=$2, left_at=$3 WHERE request_id=$1 AND status='WAITING'`,
		requestID, status, at)
	return err
}

// GetQueueEntry returns a queue entry by id.
func (q Q) GetQueueEntry(ctx context.Context, id int64) (*QueueEntry, error) {
	return one[QueueEntry](ctx, q.db, `SELECT *, 0 AS position FROM queue_entries WHERE id=$1`, id)
}

// =============================================================================
// Buffer ("tampon"): requests waiting for a retry after a timeout
// =============================================================================

// UpsertBuffer creates the pending buffer entry of a request, or updates the
// existing one (new stage, error, attempt counter).
func (q Q) UpsertBuffer(ctx context.Context, requestID int64, email string, stage domain.BufferStage, lastErr string, attempt bool, at time.Time) (*BufferEntry, error) {
	inc := 0
	if attempt {
		inc = 1
	}
	_, err := q.db.Exec(ctx, `INSERT INTO buffer_entries (request_id, user_email, stage, created_at, last_error, attempts, last_attempt_at)
		VALUES ($1,$2,$3,$4,$5,$6,$4)
		ON CONFLICT (user_email) WHERE status='PENDING' DO UPDATE
		SET stage=EXCLUDED.stage, last_error=EXCLUDED.last_error,
		    attempts=buffer_entries.attempts+$6, last_attempt_at=EXCLUDED.last_attempt_at`,
		requestID, email, stage, at, lastErr, inc)
	if err != nil {
		return nil, err
	}
	return q.PendingBuffer(ctx, email)
}

// PendingBuffer returns the pending buffer entry of a user with its position.
func (q Q) PendingBuffer(ctx context.Context, email string) (*BufferEntry, error) {
	return one[BufferEntry](ctx, q.db, `SELECT * FROM (
		SELECT *, row_number() OVER (ORDER BY created_at, id)::int AS position
		FROM buffer_entries WHERE status='PENDING') t WHERE user_email=$1`, email)
}

// ListPendingBuffer returns the buffer in retry order.
func (q Q) ListPendingBuffer(ctx context.Context) ([]BufferEntry, error) {
	return many[BufferEntry](ctx, q.db, `SELECT *, row_number() OVER (ORDER BY created_at, id)::int AS position
		FROM buffer_entries WHERE status='PENDING' ORDER BY created_at, id`)
}

// ListAbandonedBuffer returns the last abandoned buffer entries.
func (q Q) ListAbandonedBuffer(ctx context.Context, limit int) ([]BufferEntry, error) {
	return many[BufferEntry](ctx, q.db, `SELECT *, 0 AS position FROM buffer_entries WHERE status='ABANDONED'
		ORDER BY resolved_at DESC LIMIT $1`, limit)
}

// BufferDepth returns the number of pending buffer entries.
func (q Q) BufferDepth(ctx context.Context) (int, error) {
	return scalar[int](ctx, q.db, `SELECT count(*) FROM buffer_entries WHERE status='PENDING'`)
}

// ResolveBuffer closes the pending buffer entry of a request.
func (q Q) ResolveBuffer(ctx context.Context, requestID int64, status domain.BufferStatus, at time.Time) error {
	_, err := q.db.Exec(ctx, `UPDATE buffer_entries SET status=$2, resolved_at=$3 WHERE request_id=$1 AND status='PENDING'`,
		requestID, status, at)
	return err
}

// GetBufferEntry returns a buffer entry by id.
func (q Q) GetBufferEntry(ctx context.Context, id int64) (*BufferEntry, error) {
	return one[BufferEntry](ctx, q.db, `SELECT *, 0 AS position FROM buffer_entries WHERE id=$1`, id)
}
