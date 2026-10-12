package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// FulfilOutcome is the result of the assignment pipeline.
type FulfilOutcome string

const (
	OutcomeAssigned FulfilOutcome = "ASSIGNED" // credentials + license granted
	OutcomeQueued   FulfilOutcome = "QUEUED"   // pool exhausted -> waiting list
	OutcomeBuffered FulfilOutcome = "BUFFERED" // timeout -> buffer, retried every 7 min
	OutcomeFailed   FulfilOutcome = "FAILED"   // permanent technical error
)

// FulfilResult is returned to the UI after steps X) Y) Z).
type FulfilResult struct {
	Outcome        FulfilOutcome      `json:"outcome"`
	RequestID      int64              `json:"requestId"`
	Message        string             `json:"message"`
	Steps          []domain.Step      `json:"steps"`
	Assignment     *store.Assignment  `json:"assignment,omitempty"`
	QueuePosition  int                `json:"queuePosition,omitempty"`
	QueueTotal     int                `json:"queueTotal,omitempty"`
	BufferPosition int                `json:"bufferPosition,omitempty"`
	BufferTotal    int                `json:"bufferTotal,omitempty"`
	BufferStage    domain.BufferStage `json:"bufferStage,omitempty"`
	EmailSent      bool               `json:"emailSent"`

	newlyQueued   bool // first time in the waiting list (triggers the e-mail)
	newlyBuffered bool // first time in the buffer (triggers the e-mail)
}

// poolCheckTimeout bounds the Google call made inside the pool critical section.
const poolCheckTimeout = 4 * time.Second

// fulfil runs the assignment pipeline and then sends the notification e-mail.
func (s *Service) fulfil(ctx context.Context, req *store.Request, src domain.AssignmentSource, actor string, priority int) (*FulfilResult, error) {
	res, err := s.fulfilSteps(ctx, req, src, actor, priority)
	if err != nil {
		return nil, err
	}
	s.notifyOutcome(ctx, req, res, false)
	return res, nil
}

// fulfilSteps is the pipeline itself:
//
//  1. pick the current subscription (none -> queue);
//  2. already licensed in Google? (e.g. a previous attempt timed out but
//     succeeded on Google's side) -> skip the reservation and the assign call;
//  3. RESERVE a seat — the only critical section (pool advisory lock, a few
//     ms + one bounded getPool call): available = Google counter minus seats
//     reserved by other in-flight / buffered requests. None -> queue;
//  4. X) credentials (IAM bundle group)   timeout -> buffer (seat kept)
//  5. Y) license assignment               timeout -> buffer (seat kept)
//  6. persist the assignment (releases the reservation), close queue / buffer.
//
// Slow Google calls never run under the lock, so one hanging call cannot
// block the other users.
func (s *Service) fulfilSteps(ctx context.Context, req *store.Request, src domain.AssignmentSource, actor string, priority int) (*FulfilResult, error) {
	email := req.UserEmail
	cur, err := s.DB.CurrentSubscription(ctx)
	if errors.Is(err, store.ErrNotFound) || (err == nil && cur.State != "ACTIVE") {
		return s.toQueue(ctx, req, priority)
	}
	if err != nil {
		return nil, err
	}

	// Is the user already licensed? A fresh enumeration is too expensive here,
	// the cached snapshot is enough (the assign call is idempotent anyway).
	alreadyLicensed := false
	if snap, err := s.pool.Get(ctx); err == nil && snap.Subscription != nil && snap.Subscription.ID == cur.ID {
		_, alreadyLicensed = snap.Has(email)
	}

	if !alreadyLicensed && req.ReservedAt == nil {
		full, err := s.reserveSeat(ctx, req, cur.ID)
		if err != nil {
			// Lock contention or getPool timeout: nothing reserved, retry later.
			if telemetry.IsTransient(err) || errors.Is(err, context.DeadlineExceeded) {
				return s.toBuffer(ctx, req, s.firstPendingStage(req), err)
			}
			return s.failRequest(ctx, req, s.firstPendingStage(req), err)
		}
		if full {
			return s.toQueue(ctx, req, priority)
		}
	}

	// X) credentials.
	if req.CredentialsAt == nil {
		if err := s.Credentials.Grant(ctx, email); err != nil {
			return s.stepError(ctx, req, domain.StageCredentials, err)
		}
		req.CredentialsAt = ptr(s.Now())
		if err := s.DB.UpdateRequest(ctx, req); err != nil {
			return nil, err
		}
	}

	// Y) license.
	if !alreadyLicensed {
		if err := s.Licensing.Assign(ctx, cur.ID, []string{email}); err != nil {
			return s.stepError(ctx, req, domain.StageLicense, err)
		}
	}
	s.pool.Invalidate()

	// Persist (detached context: the license exists in Google now, the
	// database must record it even if the user's deadline just expired).
	wctx, cancel := detached(ctx, 10*time.Second)
	defer cancel()
	now := s.Now()
	a := &store.Assignment{
		UserEmail: email, SubscriptionID: cur.ID, RequestID: &req.ID, Source: src, Duration: req.Duration,
		AssignedAt: now, ExpiresAt: req.Duration.AddTo(now),
		Site: req.DirSite, BusinessUnit: req.DirBusinessUnit, GPSCode: req.DirGPSCode,
	}
	if actor != "" {
		a.AssignedBy = &actor
	}
	err = s.DB.Tx(wctx, func(q store.Q) error {
		// A stale active record (deviation) is closed before the new one.
		if old, err := q.ActiveAssignment(wctx, email); err == nil {
			if err := q.EndAssignment(wctx, old.ID, domain.EndExternalRemoval, "system", now); err != nil {
				return err
			}
		}
		if err := q.InsertAssignment(wctx, a); err != nil {
			return err
		}
		req.Status = domain.ReqAssigned
		req.LicenseAt, req.CompletedAt, req.AssignmentID = &now, &now, &a.ID
		if err := q.UpdateRequest(wctx, req); err != nil {
			return err
		}
		if err := q.ResolveBuffer(wctx, req.ID, domain.BufferDone, now); err != nil {
			return err
		}
		return q.CloseQueueEntries(wctx, req.ID, domain.QueueServed, now)
	})
	if err != nil {
		return nil, fmt.Errorf("persist assignment: %w", err)
	}
	return &FulfilResult{
		Outcome: OutcomeAssigned, RequestID: req.ID, Assignment: a, Steps: s.stepsFor(req, nil),
		Message: "Your Gemini Code Assist license is active.",
	}, nil
}

// reserveSeat checks the availability and reserves a seat for the request
// in one short critical section. full=true means the pool is exhausted.
func (s *Service) reserveSeat(ctx context.Context, req *store.Request, subscriptionID string) (full bool, err error) {
	err = s.DB.WithLock(ctx, store.LockPool, func() error {
		pctx, cancel := context.WithTimeout(ctx, poolCheckTimeout)
		defer cancel()
		pool, err := s.Licensing.GetPool(pctx, subscriptionID)
		if err != nil {
			return err
		}
		reserved, err := s.DB.ReservedSeats(ctx, req.ID)
		if err != nil {
			return err
		}
		if pool.Available-reserved <= 0 {
			full = true
			return nil
		}
		return s.holdSeat(ctx, req)
	})
	return full, err
}

// holdSeat records a seat reservation on the request.
func (s *Service) holdSeat(ctx context.Context, req *store.Request) error {
	req.ReservedAt = ptr(s.Now())
	return s.DB.UpdateRequest(ctx, req)
}

// firstPendingStage returns the first stage not completed yet.
func (s *Service) firstPendingStage(req *store.Request) domain.BufferStage {
	if req.CredentialsAt == nil {
		return domain.StageCredentials
	}
	return domain.StageLicense
}

// stepError routes an error of a pipeline step: transient -> buffer,
// permanent -> failed request.
func (s *Service) stepError(ctx context.Context, req *store.Request, stage domain.BufferStage, err error) (*FulfilResult, error) {
	if telemetry.IsTransient(err) || errors.Is(err, context.DeadlineExceeded) {
		return s.toBuffer(ctx, req, stage, err)
	}
	return s.failRequest(ctx, req, stage, err)
}

// toBuffer stores the request in the buffer table. Called when ctx may be
// expired, hence the detached context.
func (s *Service) toBuffer(ctx context.Context, req *store.Request, stage domain.BufferStage, cause error) (*FulfilResult, error) {
	wctx, cancel := detached(ctx, 5*time.Second)
	defer cancel()
	now := s.Now()
	retry := req.Status == domain.ReqBuffered
	newly := req.BufferedAt == nil
	var buf *store.BufferEntry
	abandoned := false
	err := s.DB.Tx(wctx, func(q store.Q) error {
		var err error
		if buf, err = q.UpsertBuffer(wctx, req.ID, req.UserEmail, stage, errString(cause), retry, now); err != nil {
			return err
		}
		if err := q.CloseQueueEntries(wctx, req.ID, domain.QueueBuffered, now); err != nil {
			return err
		}
		if buf.Attempts >= s.Policy.BufferMaxAttempts {
			// Give up: release the reservation, flag the request as failed.
			abandoned = true
			if err := q.ResolveBuffer(wctx, req.ID, domain.BufferAbandoned, now); err != nil {
				return err
			}
			req.Status = domain.ReqFailed
			req.CompletedAt = &now
			req.FailureReason = ptr(fmt.Sprintf("%s: retries exhausted after %d attempts (%s)", stage, buf.Attempts, errString(cause)))
			return q.UpdateRequest(wctx, req)
		}
		req.Status = domain.ReqBuffered
		if req.BufferedAt == nil {
			req.BufferedAt = &now
		}
		return q.UpdateRequest(wctx, req)
	})
	if err != nil {
		return nil, err
	}
	if abandoned {
		slog.Error("buffer entry abandoned", "email", req.UserEmail, "stage", stage, "attempts", buf.Attempts)
		return &FulfilResult{Outcome: OutcomeFailed, RequestID: req.ID, Steps: s.stepsFor(req, nil),
			Message: "The request could not be completed after many retries. Administrators have been notified."}, nil
	}
	total, _ := s.DB.BufferDepth(wctx)
	return &FulfilResult{
		Outcome: OutcomeBuffered, RequestID: req.ID, BufferPosition: buf.Position, BufferTotal: total, BufferStage: stage, newlyBuffered: newly,
		Steps:   s.stepsFor(req, buf),
		Message: fmt.Sprintf("Google did not answer in time. Your request is saved with priority and retried automatically every %d minutes: come back in about 10 minutes.", int(s.Cfg.BufferRetryEvery.Minutes())),
	}, nil
}

// toQueue puts the request in the waiting list (idempotent).
func (s *Service) toQueue(ctx context.Context, req *store.Request, priority int) (*FulfilResult, error) {
	wctx, cancel := detached(ctx, 5*time.Second)
	defer cancel()
	now := s.Now()
	if req.Status == domain.ReqBuffered && priority < domain.PriorityBuffered {
		priority = domain.PriorityBuffered // buffered users keep their head start
	}
	newly := req.QueuedAt == nil
	var entry *store.QueueEntry
	err := s.DB.Tx(wctx, func(q store.Q) error {
		var err error
		if entry, err = q.Enqueue(wctx, req.ID, req.UserEmail, priority, now); err != nil {
			return err
		}
		if err := q.ResolveBuffer(wctx, req.ID, domain.BufferDone, now); err != nil {
			return err
		}
		req.Status = domain.ReqQueued
		req.ReservedAt = nil // a queued request holds no seat
		if req.QueuedAt == nil {
			req.QueuedAt = &now
		}
		return q.UpdateRequest(wctx, req)
	})
	if err != nil {
		return nil, err
	}
	total, _ := s.DB.QueueDepth(wctx)
	return &FulfilResult{
		Outcome: OutcomeQueued, RequestID: req.ID, QueuePosition: entry.Position, QueueTotal: total,
		Steps: s.stepsFor(req, nil), newlyQueued: newly,
		Message: fmt.Sprintf("The license pool is exhausted. Your request is validated and you are #%d in the waiting list.", entry.Position),
	}, nil
}

// failRequest marks a request as failed after a permanent error.
func (s *Service) failRequest(ctx context.Context, req *store.Request, stage domain.BufferStage, cause error) (*FulfilResult, error) {
	wctx, cancel := detached(ctx, 5*time.Second)
	defer cancel()
	now := s.Now()
	slog.Error("request failed", "email", req.UserEmail, "stage", stage, "err", cause)
	err := s.DB.Tx(wctx, func(q store.Q) error {
		req.Status = domain.ReqFailed
		req.CompletedAt = &now
		req.FailureReason = ptr(fmt.Sprintf("%s: %s", stage, errString(cause)))
		if err := q.ResolveBuffer(wctx, req.ID, domain.BufferAbandoned, now); err != nil {
			return err
		}
		if err := q.CloseQueueEntries(wctx, req.ID, domain.QueueCancelled, now); err != nil {
			return err
		}
		return q.UpdateRequest(wctx, req)
	})
	if err != nil {
		return nil, err
	}
	return &FulfilResult{Outcome: OutcomeFailed, RequestID: req.ID, Steps: s.stepsFor(req, nil),
		Message: "A technical error prevented the assignment. Please contact an administrator."}, nil
}

// sourceFor returns the waterfall category of a request being fulfilled.
func sourceFor(req *store.Request) domain.AssignmentSource {
	if req.Origin == "ADMIN" || (req.Decision != nil && *req.Decision == domain.DecisionApproved) {
		return domain.SourceAdmin
	}
	return domain.SourceAuto
}
