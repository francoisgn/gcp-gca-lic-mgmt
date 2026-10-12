package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// maxQueueBatch bounds the number of users served per queue run.
const maxQueueBatch = 50

// ProcessQueue serves the waiting list while seats are available.
func (s *Service) ProcessQueue(ctx context.Context) (string, error) {
	served, buffered, failed := 0, 0, 0
	for i := 0; i < maxQueueBatch; i++ {
		head, err := s.DB.QueueHead(ctx)
		if errors.Is(err, store.ErrNotFound) {
			break
		}
		if err != nil {
			return "", err
		}
		req, err := s.DB.GetRequest(ctx, head.RequestID)
		if err != nil {
			return "", err
		}
		if req.Status != domain.ReqQueued {
			// Inconsistent entry (request closed meanwhile): drop it.
			if err := s.DB.CloseQueueEntries(ctx, req.ID, domain.QueueCancelled, s.Now()); err != nil {
				return "", err
			}
			continue
		}
		jctx, cancel := s.jobCtx(ctx, telemetry.KindJob)
		res, err := s.fulfil(jctx, req, sourceFor(req), "", head.Priority)
		cancel()
		if err != nil {
			return "", err
		}
		switch res.Outcome {
		case OutcomeQueued:
			// No seat left: stop here, the order of the queue is preserved.
			return fmt.Sprintf("served=%d buffered=%d failed=%d (pool exhausted, %d waiting)", served, buffered, failed, res.QueueTotal), nil
		case OutcomeAssigned:
			served++
		case OutcomeBuffered:
			buffered++
		case OutcomeFailed:
			failed++
		}
	}
	return fmt.Sprintf("served=%d buffered=%d failed=%d", served, buffered, failed), nil
}

// RetryBuffer retries every pending buffer entry (oldest first), then the
// confirmation e-mails that could not be sent.
func (s *Service) RetryBuffer(ctx context.Context) (string, error) {
	entries, err := s.DB.ListPendingBuffer(ctx)
	if err != nil {
		return "", err
	}
	counts := map[string]int{}
	for _, e := range entries {
		outcome, err := s.retryEntry(ctx, e)
		if err != nil {
			slog.Error("buffer retry", "email", e.UserEmail, "err", err)
			counts["error"]++
			continue
		}
		counts[outcome]++
	}
	mails, err := s.RetryEmails(ctx)
	if err != nil {
		return "", err
	}
	if counts[string(OutcomeAssigned)] > 0 || counts[string(OutcomeQueued)] > 0 {
		s.kickQueue()
	}
	return fmt.Sprintf("entries=%d %v emails_resent=%d", len(entries), counts, mails), nil
}

// RetryBufferEntry retries one entry on demand (admin "retry now").
func (s *Service) RetryBufferEntry(ctx context.Context, admin string, id int64) (string, error) {
	e, err := s.DB.GetBufferEntry(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return "", notFound("Buffer entry not found.")
	}
	if err != nil {
		return "", err
	}
	if e.Status != domain.BufferPending {
		return "", conflict("This entry is not pending anymore.")
	}
	_ = s.DB.Audit(ctx, admin, "buffer.retry", e.UserEmail, map[string]any{"entry": id, "stage": e.Stage})
	return s.retryEntry(telemetry.WithKind(ctx, telemetry.KindAdmin), *e)
}

// retryEntry resumes the pipeline of one buffered request from its stage.
func (s *Service) retryEntry(ctx context.Context, e store.BufferEntry) (string, error) {
	req, err := s.DB.GetRequest(ctx, e.RequestID)
	if err != nil {
		return "", err
	}
	kind := telemetry.KindFromOr(ctx, telemetry.KindJob)

	if e.Stage == domain.StageDirectory {
		// Directory outages are not buffered any more (the request stops and
		// the user retries). A leftover entry is closed: back to draft.
		req.Status = domain.ReqDraft
		now := s.Now()
		return "directory_closed", s.DB.Tx(ctx, func(q store.Q) error {
			if err := q.UpdateRequest(ctx, req); err != nil {
				return err
			}
			return q.ResolveBuffer(ctx, req.ID, domain.BufferAbandoned, now)
		})
	}

	jctx, cancel := s.jobCtx(ctx, kind)
	defer cancel()
	res, err := s.fulfil(jctx, req, sourceFor(req), "", domain.PriorityBuffered)
	if err != nil {
		return "", err
	}
	return string(res.Outcome), nil
}
