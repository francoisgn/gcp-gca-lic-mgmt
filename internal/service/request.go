package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// FormInput is the content of steps a) to d) of the form.
type FormInput struct {
	TrainingAck bool              `json:"trainingAck"`
	Duration    string            `json:"duration"`
	Answers     map[string]string `json:"answers"`
}

// DirectoryResult is the answer of step W) (directory validation).
type DirectoryResult struct {
	RequestID      int64                   `json:"requestId"`
	Outcome        domain.DirectoryOutcome `json:"outcome"`
	Reason         string                  `json:"reason,omitempty"`
	Site           string                  `json:"site,omitempty"`
	BusinessUnit   string                  `json:"businessUnit,omitempty"`
	GPSCode        string                  `json:"gpsCode,omitempty"`
	BufferPosition int                     `json:"bufferPosition,omitempty"`
	BufferTotal    int                     `json:"bufferTotal,omitempty"`
	PoolExhausted  bool                    `json:"poolExhausted"`
	NextPosition   int                     `json:"nextPosition,omitempty"`
	Steps          []domain.Step           `json:"steps"`
}

// CheckDirectory validates the form, stores it as a draft request and asks
// the enterprise directory whether the user is eligible to the automatic
// assignment.
func (s *Service) CheckDirectory(ctx context.Context, user auth.Identity, in FormInput) (*DirectoryResult, error) {
	email := user.Email
	if err := s.ensureCanRequest(ctx, email); err != nil {
		return nil, err
	}

	// Server-side validation of steps a) to d).
	if !in.TrainingAck {
		return nil, badRequest("The Gemini Code Assist training must be completed first.")
	}
	dur, err := domain.ParseDuration(in.Duration)
	if err != nil || !s.Policy.AllowsDuration(dur) {
		return nil, badRequest("Please select a valid duration.")
	}
	answers, err := s.Policy.ValidateAnswers(in.Answers)
	if err != nil {
		return nil, badRequest("%s", err.Error())
	}

	// Create or refresh the draft (one open request per user).
	req, err := s.DB.OpenRequestFor(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		req = &store.Request{UserEmail: email, Status: domain.ReqDraft, TrainingAck: true, Duration: dur, Answers: answers}
		if err := s.DB.CreateRequest(ctx, req); err != nil {
			if store.IsUniqueViolation(err) {
				return nil, conflict("A request is already in progress, please refresh the page.")
			}
			return nil, err
		}
	case err != nil:
		return nil, err
	case req.Status != domain.ReqDraft && req.Status != domain.ReqDirectoryKO:
		return nil, conflict("A request is already in progress, please refresh the page.")
	default:
		req.Status, req.TrainingAck, req.Duration, req.Answers = domain.ReqDraft, true, dur, answers
	}

	var (
		acc       directory.Account
		lookupErr error
		outcome   domain.DirectoryOutcome
		reason    string
	)
	if s.Policy.IsExternal(email) {
		// External accounts are never approved automatically: straight to the
		// "functional KO" choice (escalation possible), with no dependency on
		// the directory. Its attributes are fetched best effort for the admins.
		dctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindUX), 2*time.Second)
		acc, _ = s.Directory.Lookup(dctx, email)
		cancel()
		outcome, reason = domain.DirFunctionalKO, ExternalReason
	} else {
		// Ask the directory (the user is waiting: UX timeout of the directory).
		dctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindUX), s.Cfg.DirectoryTimeout)
		acc, lookupErr = s.Directory.Lookup(dctx, email)
		cancel()
		if outcome, reason, err = s.evaluateDirectory(ctx, email, acc, lookupErr); err != nil {
			return nil, err
		}
	}

	now := s.Now()
	s.applyDirectory(req, acc, outcome, reason, now)
	res := &DirectoryResult{RequestID: req.ID, Outcome: outcome, Reason: reason,
		Site: acc.Site, BusinessUnit: acc.BusinessUnit, GPSCode: maskGPS(acc.GPSCode)}

	switch outcome {
	case domain.DirTechnicalKO:
		// Technical KO -> buffer, retried by the scheduled job.
		req.Status = domain.ReqBuffered
		req.BufferedAt = &now
		wctx, wcancel := detached(ctx, 5*time.Second)
		defer wcancel()
		var buf *store.BufferEntry
		err = s.DB.Tx(wctx, func(q store.Q) error {
			if err := q.UpdateRequest(wctx, req); err != nil {
				return err
			}
			buf, err = q.UpsertBuffer(wctx, req.ID, email, domain.StageDirectory, errString(lookupErr), false, now)
			return err
		})
		if err != nil {
			return nil, err
		}
		res.BufferPosition = buf.Position
		res.BufferTotal, _ = s.DB.BufferDepth(ctx)
		res.Steps = s.stepsFor(req, buf)
		s.notifyBuffered(ctx, req, domain.StageDirectory)
		return res, nil
	case domain.DirFunctionalKO:
		req.Status = domain.ReqDirectoryKO
	}
	if err := s.DB.UpdateRequest(ctx, req); err != nil {
		return nil, err
	}
	res.Steps = s.stepsFor(req, nil)
	if outcome == domain.DirOK {
		if snap, err := s.pool.Get(ctx); err == nil {
			reserved, _ := s.DB.ReservedSeats(ctx, 0)
			if snap.Pool.Available-reserved <= 0 {
				res.PoolExhausted = true
				depth, _ := s.DB.QueueDepth(ctx)
				res.NextPosition = depth + 1
			}
		}
	}
	return res, nil
}

// ExternalReason is shown to external accounts (never auto-approved).
const ExternalReason = "External accounts are never approved automatically. You can escalate your request to an administrator."

// evaluateDirectory turns a directory answer into an outcome.
func (s *Service) evaluateDirectory(ctx context.Context, email string, acc directory.Account, err error) (domain.DirectoryOutcome, string, error) {
	switch {
	case err == nil:
		ok, reason, eerr := s.eligible(ctx, email, acc)
		if eerr != nil {
			return "", "", eerr
		}
		if !ok {
			return domain.DirFunctionalKO, reason, nil
		}
		return domain.DirOK, "", nil
	case directory.IsNotFound(err):
		return domain.DirFunctionalKO, "Your account was not found in the enterprise directory.", nil
	default:
		// Timeout, 5xx, network, misconfiguration: not the user's fault.
		return domain.DirTechnicalKO, "The enterprise directory did not answer in time.", nil
	}
}

// eligible applies the automatic approval rules: not an external account,
// directory status and GPS code format (policy.yaml), GPS code present in the
// allowlist managed by the administrators (database).
func (s *Service) eligible(ctx context.Context, email string, acc directory.Account) (bool, string, error) {
	if s.Policy.IsExternal(email) {
		return false, ExternalReason, nil
	}
	if ok, reason := s.Policy.Eligibility.Evaluate(acc.Status, acc.BusinessUnit, acc.GPSCode); !ok {
		return false, reason, nil
	}
	allowed, err := s.DB.IsGPSAllowed(ctx, acc.GPSCode)
	if err != nil {
		return false, "", err
	}
	if !allowed {
		return false, fmt.Sprintf("Your GPS code (%s) is not in the list of codes authorized for automatic approval.", maskGPS(acc.GPSCode)), nil
	}
	return true, "", nil
}

// applyDirectory copies the directory answer into the request.
func (s *Service) applyDirectory(req *store.Request, acc directory.Account, outcome domain.DirectoryOutcome, reason string, now time.Time) {
	req.DirOutcome = &outcome
	req.DirCheckedAt = &now
	req.DirReason = nil
	if reason != "" {
		req.DirReason = &reason
	}
	if acc.Email != "" {
		req.DirAccountStatus, req.DirSite = &acc.Status, &acc.Site
		req.DirBusinessUnit, req.DirGPSCode = &acc.BusinessUnit, &acc.GPSCode
	}
}

// ensureCanRequest refuses a new request when the user already holds a
// license or has a request in a non-editable state.
func (s *Service) ensureCanRequest(ctx context.Context, email string) error {
	if snap, err := s.pool.Get(ctx); err == nil {
		if _, ok := snap.Has(email); ok {
			return conflict("You already hold a Gemini Code Assist license.")
		}
	}
	if _, err := s.DB.PendingBuffer(ctx, email); err == nil {
		return conflict("Your previous request is being processed, please come back later.")
	}
	return nil
}

// EscalationInput is step W1) (escalation to an administrator).
type EscalationInput struct {
	Division string `json:"division"`
	Entity   string `json:"entity"`
	Project  string `json:"project"`
	Comment  string `json:"comment"`
}

// Escalate sends a request in functional KO to the administrators.
func (s *Service) Escalate(ctx context.Context, user auth.Identity, requestID int64, in EscalationInput) (*store.Request, error) {
	in.Division, in.Entity = strings.TrimSpace(in.Division), strings.TrimSpace(in.Entity)
	in.Project, in.Comment = strings.TrimSpace(in.Project), strings.TrimSpace(in.Comment)
	if in.Division == "" || in.Entity == "" || in.Project == "" {
		return nil, badRequest("Division, entity and project are required.")
	}
	for _, f := range []string{in.Division, in.Entity, in.Project} {
		if utf8.RuneCountInString(f) > 120 {
			return nil, badRequest("Fields are limited to 120 characters.")
		}
	}
	if utf8.RuneCountInString(in.Comment) > 2000 {
		return nil, badRequest("The comment is limited to 2000 characters.")
	}
	var req *store.Request
	err := s.DB.Tx(ctx, func(q store.Q) error {
		var err error
		if req, err = s.ownedRequest(ctx, q, user, requestID); err != nil {
			return err
		}
		if req.Status != domain.ReqDirectoryKO {
			return conflict("This request cannot be escalated anymore.")
		}
		now := s.Now()
		req.Status = domain.ReqPendingAdmin
		req.EscalatedAt = &now
		req.EscDivision, req.EscEntity, req.EscProject = &in.Division, &in.Entity, &in.Project
		if in.Comment != "" {
			req.EscComment = &in.Comment
		}
		return q.UpdateRequest(ctx, req)
	})
	if err != nil {
		return nil, err
	}
	s.notifyEscalated(ctx, req)
	return req, nil
}

// ExitRequest closes a request in functional KO at the user's choice.
func (s *Service) ExitRequest(ctx context.Context, user auth.Identity, requestID int64) error {
	return s.DB.Tx(ctx, func(q store.Q) error {
		req, err := s.ownedRequest(ctx, q, user, requestID)
		if err != nil {
			return err
		}
		if req.Status != domain.ReqDirectoryKO && req.Status != domain.ReqDraft {
			return conflict("This request cannot be closed anymore.")
		}
		req.Status = domain.ReqAbandoned
		req.CompletedAt = ptr(s.Now())
		req.FailureReason = ptr("Closed by the user after the directory validation")
		return q.UpdateRequest(ctx, req)
	})
}

// Submit runs steps X) Y) Z) for a request validated by the directory. The
// user waits in front of a loading animation: the whole operation is bounded
// by the UX timeout (10 s); on timeout the request goes to the buffer.
func (s *Service) Submit(ctx context.Context, user auth.Identity, requestID int64) (*FulfilResult, error) {
	req, err := s.ownedRequest(ctx, s.DB.Q, user, requestID)
	if err != nil {
		return nil, err
	}
	if req.Status != domain.ReqDraft || req.DirOutcome == nil || *req.DirOutcome != domain.DirOK {
		return nil, conflict("This request is not ready for submission, please refresh the page.")
	}
	if req.DirCheckedAt == nil || s.Now().Sub(*req.DirCheckedAt) > time.Hour {
		return nil, conflict("The directory validation is too old, please validate again.")
	}
	uctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindUX), s.Cfg.UXTimeout)
	defer cancel()
	return s.fulfil(uctx, req, domain.SourceAuto, "", domain.PriorityNormal)
}

// LeaveQueue removes the user from the waiting list.
func (s *Service) LeaveQueue(ctx context.Context, user auth.Identity) error {
	return s.DB.Tx(ctx, func(q store.Q) error {
		req, err := q.OpenRequestFor(ctx, user.Email)
		if errors.Is(err, store.ErrNotFound) || (err == nil && req.Status != domain.ReqQueued) {
			return conflict("You are not in the waiting list.")
		}
		if err != nil {
			return err
		}
		now := s.Now()
		if err := q.CloseQueueEntries(ctx, req.ID, domain.QueueLeft, now); err != nil {
			return err
		}
		req.Status = domain.ReqAbandoned
		req.CompletedAt = &now
		req.FailureReason = ptr("Left the waiting list")
		return q.UpdateRequest(ctx, req)
	})
}

// ownedRequest loads a request and checks it belongs to the user.
func (s *Service) ownedRequest(ctx context.Context, q store.Q, user auth.Identity, id int64) (*store.Request, error) {
	req, err := q.GetRequestForUpdate(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, notFound("Request not found.")
	}
	if err != nil {
		return nil, err
	}
	if req.UserEmail != user.Email {
		return nil, forbidden("This request belongs to another user.")
	}
	return req, nil
}

// maskGPS hides the middle of a GPS code ("a3••••1e").
func maskGPS(code string) string {
	if len(code) < 6 {
		return code
	}
	return code[:2] + strings.Repeat("•", len(code)-4) + code[len(code)-2:]
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
