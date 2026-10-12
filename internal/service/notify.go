package service

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/policy"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// =============================================================================
// Notification events — one e-mail per step change. Each event can be
// disabled in policy.yaml (notifications). Every attempt is journaled in the
// notifications table (Admin → Notifications), including disabled ones.
//
// Admin approval: ONE e-mail when the license is assigned right away
// ("license active", mentioning the approval); TWO when it is not
// (approval with the queue position / "in progress", then "license active").
// =============================================================================
const (
	EvRequestEscalated    = "request_escalated"    // user: request under administrator review
	EvAdminEscalation     = "admin_new_escalation" // administrators: new escalation to review
	EvRequestApproved     = "request_approved"     // user: approved, license not active yet
	EvRequestRejected     = "request_rejected"     // user: rejected
	EvLicenseAssigned     = "license_assigned"     // user: license active (end of the process)
	EvQueued              = "queued"               // user: pool exhausted, waiting list position
	EvBuffered            = "buffered"             // user: timeout, automatic retry
	EvRemovedUnused       = "removed_unused"       // user: 7 days without usage
	EvRemovedExpired      = "removed_expired"      // user: requested duration elapsed
	EvRemovedAdmin        = "removed_admin"        // user: removed by an administrator
	EvRemovedSubscription = "removed_subscription" // user: no seat in the renewed subscription
)

// Mail template data. Field names are used by internal/mail/templates.
type mailBase struct {
	Name      string
	Email     string
	PortalURL string
	Links     policy.Links
}

type assignedMail struct {
	mailBase
	RequestDate   time.Time
	AssignDate    time.Time
	EndDate       time.Time
	DurationLabel string
	ProjectID     string
	UnusedDays    int
	ApprovedBy    string // set when an administrator approved the request
}

type approvedMail struct {
	mailBase
	ApprovedBy string
	Queued     bool
	Position   int
}

type removedMail struct {
	mailBase
	Since      time.Time
	Until      time.Time
	Reason     string
	ReasonHint string
	Requeued   bool // subscription renewal: back in the waiting list
	Position   int  // position in the waiting list when requeued
	Waiting    int  // users waiting
}

type queuedMail struct {
	mailBase
	Position int
}

type bufferedMail struct {
	mailBase
	Step       string
	RetryEvery string
}

type rejectedMail struct {
	mailBase
	DecidedAt *time.Time
	Reason    string
}

type escalatedMail struct {
	mailBase
	Reason string
	Admins []string
}

type adminEscalationMail struct {
	mailBase
	Requester string
	Reason    string
	Division  string
	Entity    string
	Project   string
	Comment   string
	External  bool
}

func (s *Service) base(email string) mailBase {
	return mailBase{
		Name: auth.Identity{Email: email}.DisplayName(), Email: email,
		PortalURL: s.Cfg.BaseURL, Links: s.Policy.Links,
	}
}

// notify renders and sends one e-mail if the event is enabled, and journals
// the attempt. It never fails the caller: e-mail is best effort (the license
// confirmation is retried by the buffer job).
func (s *Service) notify(ctx context.Context, event, tpl, to, subject string, data any, requestID *int64) bool {
	n := store.Notification{Event: event, Recipient: to, Subject: subject, RequestID: requestID}
	defer func() {
		wctx, cancel := detached(ctx, 5*time.Second)
		defer cancel()
		if err := s.DB.InsertNotification(wctx, n); err != nil {
			slog.Error("journal notification", "err", err)
		}
	}()
	if !s.Policy.Notify(event) {
		n.Status = "DISABLED"
		return false
	}
	msg, err := s.Mails.Render(tpl, to, subject, data)
	if err != nil {
		slog.Error("render e-mail", "template", tpl, "err", err)
		n.Status, n.Error = "FAILED", ptr(err.Error())
		return false
	}
	n.BodyText = msg.Text
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	err = s.Recorder.Observe(mctx, telemetry.TargetMail, event, func(ctx context.Context) error {
		return s.Mailer.Send(ctx, msg)
	})
	if err != nil {
		n.Status, n.Error = "FAILED", ptr(err.Error())
		return false
	}
	n.Status = "SENT"
	return true
}

// notifyOutcome sends the e-mail matching a pipeline outcome. approval is
// true when the pipeline runs right after an administrator approval.
func (s *Service) notifyOutcome(ctx context.Context, req *store.Request, res *FulfilResult, approval bool) {
	switch res.Outcome {
	case OutcomeAssigned:
		res.EmailSent = s.sendAssigned(ctx, req, res.Assignment)
		res.Steps = s.stepsFor(req, nil)
	case OutcomeQueued:
		switch {
		case approval:
			s.notify(ctx, EvRequestApproved, "approved", req.UserEmail, "Your Gemini Code Assist request is approved",
				approvedMail{mailBase: s.base(req.UserEmail), ApprovedBy: approverName(req), Queued: true, Position: res.QueuePosition}, &req.ID)
		case res.newlyQueued:
			s.notify(ctx, EvQueued, "queued", req.UserEmail, "You are in the Gemini Code Assist waiting list",
				queuedMail{mailBase: s.base(req.UserEmail), Position: res.QueuePosition}, &req.ID)
		}
	case OutcomeBuffered:
		switch {
		case approval:
			s.notify(ctx, EvRequestApproved, "approved", req.UserEmail, "Your Gemini Code Assist request is approved",
				approvedMail{mailBase: s.base(req.UserEmail), ApprovedBy: approverName(req)}, &req.ID)
		case res.newlyBuffered:
			s.notifyBuffered(ctx, req, res.BufferStage)
		}
	}
}

// notifyBuffered tells the user that a service timed out and that the
// request is retried automatically (sent once per request).
func (s *Service) notifyBuffered(ctx context.Context, req *store.Request, stage domain.BufferStage) {
	step := map[domain.BufferStage]string{
		domain.StageCredentials: "cloud credentials", domain.StageLicense: "license assignment",
	}[stage]
	s.notify(ctx, EvBuffered, "buffered", req.UserEmail, "Your Gemini Code Assist request is delayed",
		bufferedMail{mailBase: s.base(req.UserEmail), Step: step, RetryEvery: strconv.Itoa(int(s.Cfg.BufferRetryEvery.Minutes())) + " minutes"}, &req.ID)
}

// notifyEscalated confirms the escalation to the user and alerts the
// administrators (super admins excluded, as in the contact list).
func (s *Service) notifyEscalated(ctx context.Context, req *store.Request) {
	admins := s.Roles.Admins()
	s.notify(ctx, EvRequestEscalated, "escalated", req.UserEmail, "Your Gemini Code Assist request is under review",
		escalatedMail{mailBase: s.base(req.UserEmail), Reason: strOr(req.DirReason, ""), Admins: admins}, &req.ID)
	for _, admin := range admins {
		s.notify(ctx, EvAdminEscalation, "admin_escalation", admin, "[GCA portal] New request to review: "+req.UserEmail,
			adminEscalationMail{mailBase: s.base(admin), Requester: req.UserEmail, Reason: strOr(req.DirReason, ""),
				Division: strOr(req.EscDivision, ""), Entity: strOr(req.EscEntity, ""), Project: strOr(req.EscProject, ""),
				Comment: strOr(req.EscComment, ""), External: s.Policy.IsExternal(req.UserEmail)}, &req.ID)
	}
}

// sendAssigned sends step Z) (recap e-mail) and records it on the request.
func (s *Service) sendAssigned(ctx context.Context, req *store.Request, a *store.Assignment) bool {
	if a == nil {
		return false
	}
	ok := s.notify(ctx, EvLicenseAssigned, "assigned", req.UserEmail, "Your Gemini Code Assist license is active", assignedMail{
		mailBase: s.base(req.UserEmail), RequestDate: req.CreatedAt, AssignDate: a.AssignedAt, EndDate: a.ExpiresAt,
		DurationLabel: a.Duration.Label(), ProjectID: s.Cfg.GCAProjectID, UnusedDays: s.Policy.UnusedDays,
		ApprovedBy: approverName(req),
	}, &req.ID)
	if !ok {
		return false // retried by the buffer job (AssignedWithoutEmail) unless disabled
	}
	now := s.Now()
	req.EmailAt = &now
	wctx, cancel := detached(ctx, 5*time.Second)
	defer cancel()
	if err := s.DB.UpdateRequest(wctx, req); err != nil {
		slog.Error("record e-mail sent", "err", err)
	}
	return true
}

// approverName returns the display name of the approving administrator.
func approverName(req *store.Request) string {
	if req == nil || req.Decision == nil || *req.Decision != domain.DecisionApproved || req.DecidedBy == nil || req.Origin != "FORM" {
		return ""
	}
	return auth.Identity{Email: *req.DecidedBy}.DisplayName()
}

// notifyRemoved informs users that their license was removed.
func (s *Service) notifyRemoved(ctx context.Context, removed []store.Assignment, reason domain.EndReason) {
	event, hint := "", ""
	switch reason {
	case domain.EndUnused:
		event, hint = EvRemovedUnused, "the license was not used for the last "+strconv.Itoa(s.Policy.UnusedDays)+" days"
	case domain.EndExpired:
		event, hint = EvRemovedExpired, "the requested duration has elapsed"
	case domain.EndAdminRemoval:
		event, hint = EvRemovedAdmin, "an administrator removed it"
	case domain.EndSubscriptionEnded:
		event, hint = EvRemovedSubscription, "the new subscription has fewer seats than the previous one; the seats went to the most recent users"
	default:
		return // removed outside the portal: no e-mail
	}
	now := s.Now()
	waiting := 0
	if reason == domain.EndSubscriptionEnded {
		waiting, _ = s.DB.QueueDepth(ctx)
	}
	for _, a := range removed {
		m := removedMail{
			mailBase: s.base(a.UserEmail), Since: a.AssignedAt, Until: now, Reason: reason.Label(), ReasonHint: hint,
			Requeued: reason == domain.EndSubscriptionEnded, Waiting: waiting,
		}
		subject := "Your Gemini Code Assist license was removed"
		if m.Requeued {
			if e, err := s.DB.WaitingEntry(ctx, a.UserEmail); err == nil {
				m.Position = e.Position
			}
			subject = "Your Gemini Code Assist license was not renewed — you are in the waiting list"
		}
		s.notify(ctx, event, "removed", a.UserEmail, subject, m, a.RequestID)
	}
}

// RetryEmails re-sends the confirmation e-mails that failed.
func (s *Service) RetryEmails(ctx context.Context) (int, error) {
	if !s.Policy.Notify(EvLicenseAssigned) {
		return 0, nil
	}
	reqs, err := s.DB.AssignedWithoutEmail(ctx, s.Now().AddDate(0, 0, -7))
	if err != nil {
		return 0, err
	}
	sent := 0
	for i := range reqs {
		r := &reqs[i]
		if r.AssignmentID == nil {
			continue
		}
		a, err := s.DB.ActiveAssignment(ctx, r.UserEmail)
		if err != nil || a.ID != *r.AssignmentID {
			continue
		}
		if s.sendAssigned(ctx, r, a) {
			sent++
		}
	}
	return sent, nil
}

// Notifications returns the latest journal entries ("" = everyone).
func (s *Service) Notifications(ctx context.Context, recipient string) ([]store.Notification, error) {
	return s.DB.ListNotifications(ctx, domain.NormalizeEmail(recipient), 200)
}
