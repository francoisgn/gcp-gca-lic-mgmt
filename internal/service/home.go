package service

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
)

// Home page states (one per case of the functional specification).
const (
	StateActive       = "ACTIVE"        // license active
	StateBuffered     = "BUFFERED"      // in the buffer (timeout), priority assignment
	StatePendingAdmin = "PENDING_ADMIN" // case 0.1
	StateRejected     = "REJECTED"      // case 0.2
	StateQueued       = "QUEUED"        // case 1
	StateDirectoryKO  = "DIRECTORY_KO"  // functional KO waiting for exit / escalation
	StateFailed       = "FAILED"        // last request failed technically
	StateNew          = "NEW"           // case 2 (pool available or exhausted)
)

// RejectedPageSize is the number of rejected requests per page.
const RejectedPageSize = 10

// ActiveLicense describes the license of the current user.
type ActiveLicense struct {
	Since         time.Time
	Until         *time.Time
	LastUsage     *time.Time
	PurgeAt       time.Time // date of removal if still unused
	DurationLabel string
	Source        string
	Subscription  string
	KnownInDB     bool
}

// HomeView is everything the landing page needs.
type HomeView struct {
	User          auth.Identity
	State         string
	Counter       PoolCounter
	Subscription  *store.Subscription
	Active        *ActiveLicense
	Buffer        *store.BufferEntry
	BufferTotal   int
	Queue         *store.QueueEntry
	QueueTotal    int
	PoolExhausted bool
	NextPosition  int            // position in the queue if the user continues
	Request       *store.Request // open request, or the latest one
	History       []store.Assignment
	TotalRequests int
	Rejections    int
	Rejected      []store.Request
	RejectedPage  int
	RejectedPages int
	Steps         []domain.Step
	Admins        []string
	Degraded      bool // Google unreachable: data partly from the database
	UnusedDays    int
	RetryEvery    time.Duration
}

// Home computes the landing page of a user.
func (s *Service) Home(ctx context.Context, user auth.Identity, rejectedPage int) (*HomeView, error) {
	email := user.Email
	v := &HomeView{User: user, Admins: s.Roles.Admins(), UnusedDays: s.Policy.UnusedDays, RetryEvery: s.Cfg.BufferRetryEvery}

	snap, err := s.pool.Get(ctx)
	if err != nil {
		slog.Warn("home: google pool unavailable", "err", err)
		v.Degraded = true
	} else {
		v.Degraded = snap.Stale
		v.Subscription = snap.Subscription
	}
	v.Counter = s.Counter(ctx)

	dbActive, err := s.DB.ActiveAssignment(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	gUser, licensed := snap.Has(email)
	if snap == nil && dbActive != nil {
		licensed = true // degraded mode: trust the database
	}

	// --- Active license ---------------------------------------------------------
	if licensed {
		v.State = StateActive
		al := &ActiveLicense{Since: gUser.AssignTime, LastUsage: gUser.RecentUsage}
		if snap != nil && snap.Subscription != nil {
			al.Subscription = snap.Subscription.ShortID()
		}
		var req *store.Request
		if dbActive != nil {
			al.KnownInDB = true
			al.Since = dbActive.AssignedAt
			al.Until = &dbActive.ExpiresAt
			al.DurationLabel = dbActive.Duration.Label()
			al.Source = string(dbActive.Source)
			if al.LastUsage == nil {
				al.LastUsage = dbActive.LastUsageAt
			}
			if dbActive.RequestID != nil {
				req, _ = s.DB.GetRequest(ctx, *dbActive.RequestID)
			}
		}
		base := al.Since
		if dbActive != nil && dbActive.MigratedAt != nil && dbActive.MigratedAt.After(base) {
			base = *dbActive.MigratedAt
		}
		if al.LastUsage != nil && al.LastUsage.After(base) {
			base = *al.LastUsage
		}
		al.PurgeAt = base.AddDate(0, 0, s.Policy.UnusedDays)
		v.Active = al
		v.Request = req
		v.Steps = allDone(req)
		if dbActive != nil && dbActive.BackportedAt != nil {
			v.Steps = s.backportedSteps(dbActive)
		}
		return v, nil
	}

	// --- Not licensed: history and counters ----------------------------------------
	if v.History, err = s.DB.AssignmentHistory(ctx, email, 10); err != nil {
		return nil, err
	}
	if v.TotalRequests, v.Rejections, err = s.DB.RequestCounts(ctx, email); err != nil {
		return nil, err
	}

	// Buffer first: it has priority over everything else.
	if buf, err := s.DB.PendingBuffer(ctx, email); err == nil {
		v.State = StateBuffered
		v.Buffer = buf
		v.BufferTotal, _ = s.DB.BufferDepth(ctx)
		v.Request, _ = s.DB.GetRequest(ctx, buf.RequestID)
		v.Steps = s.stepsFor(v.Request, buf)
		return v, nil
	}

	open, err := s.DB.OpenRequestFor(ctx, email)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if open != nil {
		v.Request = open
		v.Steps = s.stepsFor(open, nil)
		switch open.Status {
		case domain.ReqPendingAdmin:
			v.State = StatePendingAdmin
			return v, s.loadRejected(ctx, v, rejectedPage)
		case domain.ReqQueued:
			v.State = StateQueued
			if q, err := s.DB.WaitingEntry(ctx, email); err == nil {
				v.Queue = q
			}
			v.QueueTotal, _ = s.DB.QueueDepth(ctx)
			return v, nil
		case domain.ReqDirectoryKO:
			v.State = StateDirectoryKO
			return v, nil
		}
		// DRAFT: fall through to the standard process, answers are pre-filled.
	}

	if latest, err := s.DB.LatestRequestFor(ctx, email); err == nil && open == nil {
		switch latest.Status {
		case domain.ReqRejected:
			v.State = StateRejected
			v.Request = latest
			v.Steps = s.stepsFor(latest, nil)
			if err := s.loadRejected(ctx, v, rejectedPage); err != nil {
				return nil, err
			}
		case domain.ReqFailed:
			v.State = StateFailed
			v.Request = latest
			v.Steps = s.stepsFor(latest, nil)
		}
	}
	if v.State == "" {
		v.State = StateNew
		v.Steps = domain.NewSteps()
		domain.SetStep(v.Steps, domain.StepIdentity, domain.StepActive, "")
	}

	// Pool exhausted? (case 2.1) — seats reserved by the buffer count as used.
	if snap != nil {
		reserved, _ := s.DB.ReservedSeats(ctx, 0)
		if snap.Pool.Available-reserved <= 0 {
			v.PoolExhausted = true
			depth, _ := s.DB.QueueDepth(ctx)
			v.NextPosition = depth + 1
		}
	}
	return v, nil
}

// loadRejected loads a page of rejected requests (case 0.1 / 0.2).
func (s *Service) loadRejected(ctx context.Context, v *HomeView, page int) error {
	if v.Rejections == 0 {
		return nil
	}
	v.RejectedPages = (v.Rejections + RejectedPageSize - 1) / RejectedPageSize
	if page < 1 {
		page = 1
	}
	if page > v.RejectedPages {
		page = v.RejectedPages
	}
	v.RejectedPage = page
	var err error
	v.Rejected, err = s.DB.RejectedRequests(ctx, v.User.Email, RejectedPageSize, (page-1)*RejectedPageSize)
	return err
}

// allDone returns the steps of an active license: everything green, admin
// validation green only if the request was escalated.
func allDone(req *store.Request) []domain.Step {
	steps := domain.NewSteps()
	for i := range steps {
		steps[i].State = domain.StepOK
	}
	domain.SetStep(steps, domain.StepAdmin, domain.StepSkipped, "")
	if req != nil && req.WasEscalated() {
		domain.SetStep(steps, domain.StepAdmin, domain.StepOK, "")
	}
	if req != nil && req.Origin != "FORM" {
		domain.SetStep(steps, domain.StepTraining, domain.StepSkipped, "")
		domain.SetStep(steps, domain.StepDetails, domain.StepSkipped, "")
		domain.SetStep(steps, domain.StepDirectory, domain.StepSkipped, "")
	}
	if req != nil && req.EmailAt == nil && req.Status == domain.ReqAssigned {
		domain.SetStep(steps, domain.StepEmail, domain.StepWarn, "E-mail will be retried")
	}
	return steps
}

// backportedSteps is the path of a license taken over from Google (previous
// tool, console): no form step was done, the directory was checked at the
// back-port, an administrator validated it.
func (s *Service) backportedSteps(a *store.Assignment) []domain.Step {
	steps := domain.NewSteps()
	for _, id := range []domain.StepID{domain.StepIdentity, domain.StepTraining, domain.StepDuration, domain.StepDetails} {
		domain.SetStep(steps, id, domain.StepKO, "Not done (license back-ported)")
	}
	switch {
	case a.IneligibleReason != nil:
		domain.SetStep(steps, domain.StepDirectory, domain.StepKO, *a.IneligibleReason)
	case a.DirCheckResult != nil && *a.DirCheckResult != "ERROR":
		domain.SetStep(steps, domain.StepDirectory, domain.StepOK, "")
	default:
		domain.SetStep(steps, domain.StepDirectory, domain.StepTodo, "")
	}
	domain.SetStep(steps, domain.StepAdmin, domain.StepOK, "Back-ported by an administrator on "+a.BackportedAt.In(s.Cfg.TimeZone).Format("2 Jan 2006"))
	domain.SetStep(steps, domain.StepCredentials, domain.StepOK, "")
	domain.SetStep(steps, domain.StepLicense, domain.StepOK, "")
	domain.SetStep(steps, domain.StepEmail, domain.StepSkipped, "No e-mail for a back-port")
	return steps
}

// stepsFor computes the left panel from a request and its buffer entry.
func (s *Service) stepsFor(r *store.Request, buf *store.BufferEntry) []domain.Step {
	st := domain.NewSteps()
	if r == nil {
		domain.SetStep(st, domain.StepIdentity, domain.StepActive, "")
		return st
	}
	ok := func(id domain.StepID) { domain.SetStep(st, id, domain.StepOK, "") }
	ok(domain.StepIdentity)
	if r.Origin == "FORM" {
		ok(domain.StepTraining)
		ok(domain.StepDetails)
	} else {
		domain.SetStep(st, domain.StepTraining, domain.StepSkipped, "")
		domain.SetStep(st, domain.StepDetails, domain.StepSkipped, "")
	}
	ok(domain.StepDuration)

	// Directory validation.
	switch {
	case r.Origin != "FORM" && r.DirOutcome == nil:
		domain.SetStep(st, domain.StepDirectory, domain.StepSkipped, "")
	case r.DirOutcome == nil:
		domain.SetStep(st, domain.StepDirectory, domain.StepActive, "")
	case *r.DirOutcome == domain.DirOK:
		ok(domain.StepDirectory)
	case *r.DirOutcome == domain.DirFunctionalKO:
		domain.SetStep(st, domain.StepDirectory, domain.StepKO, strOr(r.DirReason, ""))
	case *r.DirOutcome == domain.DirTechnicalKO:
		domain.SetStep(st, domain.StepDirectory, domain.StepWarn, "Directory unavailable — try again later")
	}

	// Admin validation (grey unless escalated).
	if r.WasEscalated() {
		switch {
		case r.Decision == nil:
			domain.SetStep(st, domain.StepAdmin, domain.StepActive, "Waiting for an administrator")
		case *r.Decision == domain.DecisionApproved:
			domain.SetStep(st, domain.StepAdmin, domain.StepOK, "")
		default:
			domain.SetStep(st, domain.StepAdmin, domain.StepKO, "Rejected")
		}
	} else if r.Origin == "ADMIN" {
		domain.SetStep(st, domain.StepAdmin, domain.StepOK, "Assigned by an administrator")
	}

	if r.CredentialsAt != nil {
		ok(domain.StepCredentials)
	}
	if r.LicenseAt != nil {
		ok(domain.StepLicense)
	}
	if r.EmailAt != nil {
		ok(domain.StepEmail)
	}
	if r.Status == domain.ReqQueued {
		domain.SetStep(st, domain.StepLicense, domain.StepActive, "Waiting list")
	}
	if r.Status == domain.ReqAssigned && r.EmailAt == nil {
		domain.SetStep(st, domain.StepEmail, domain.StepWarn, "E-mail will be retried")
	}
	if buf != nil {
		hint := "Timeout — automatic retry scheduled"
		switch buf.Stage {
		case domain.StageCredentials:
			domain.SetStep(st, domain.StepCredentials, domain.StepWarn, hint)
		case domain.StageLicense:
			domain.SetStep(st, domain.StepLicense, domain.StepWarn, hint)
		}
	}
	if r.Status == domain.ReqFailed {
		stage, _, _ := strings.Cut(strOr(r.FailureReason, ""), ":")
		switch domain.BufferStage(stage) {
		case domain.StageCredentials:
			domain.SetStep(st, domain.StepCredentials, domain.StepKO, "Technical failure")
		default:
			domain.SetStep(st, domain.StepLicense, domain.StepKO, "Technical failure")
		}
	}
	return st
}
