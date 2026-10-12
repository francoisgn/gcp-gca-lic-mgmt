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
}

type removedMail struct {
	mailBase
	Since      time.Time
	Until      time.Time
	Reason     string
	ReasonHint string
}

type queuedMail struct {
	mailBase
	Position int
}

type rejectedMail struct {
	mailBase
	DecidedAt *time.Time
	Reason    string
}

type actionMail struct {
	mailBase
	Reason string
}

func (s *Service) base(email string) mailBase {
	return mailBase{
		Name: auth.Identity{Email: email}.DisplayName(), Email: email,
		PortalURL: s.Cfg.BaseURL, Links: s.Policy.Links,
	}
}

// send renders and sends one e-mail, recording it in telemetry.
func (s *Service) send(ctx context.Context, tpl, to, subject string, data any) error {
	msg, err := s.Mails.Render(tpl, to, subject, data)
	if err != nil {
		slog.Error("render e-mail", "template", tpl, "err", err)
		return err
	}
	mctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	return s.Recorder.Observe(mctx, telemetry.TargetMail, tpl, func(ctx context.Context) error {
		return s.Mailer.Send(ctx, msg)
	})
}

// notifyOutcome sends the e-mail matching a pipeline outcome.
func (s *Service) notifyOutcome(ctx context.Context, req *store.Request, res *FulfilResult) {
	switch res.Outcome {
	case OutcomeAssigned:
		res.EmailSent = s.sendAssigned(ctx, req, res.Assignment)
		res.Steps = s.stepsFor(req, nil)
	case OutcomeQueued:
		if res.newlyQueued {
			_ = s.send(ctx, "queued", req.UserEmail, "You are in the Gemini Code Assist waiting list",
				queuedMail{mailBase: s.base(req.UserEmail), Position: res.QueuePosition})
		}
	}
}

// sendAssigned sends step Z) (recap e-mail) and records it on the request.
func (s *Service) sendAssigned(ctx context.Context, req *store.Request, a *store.Assignment) bool {
	if a == nil {
		return false
	}
	err := s.send(ctx, "assigned", req.UserEmail, "Your Gemini Code Assist license is active", assignedMail{
		mailBase: s.base(req.UserEmail), RequestDate: req.CreatedAt, AssignDate: a.AssignedAt, EndDate: a.ExpiresAt,
		DurationLabel: a.Duration.Label(), ProjectID: s.Cfg.GCAProjectID, UnusedDays: s.Policy.UnusedDays,
	})
	if err != nil {
		return false // retried by the buffer job (AssignedWithoutEmail)
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

// notifyRemoved informs users that their license was removed.
func (s *Service) notifyRemoved(ctx context.Context, removed []store.Assignment, reason domain.EndReason) {
	hint := ""
	switch reason {
	case domain.EndUnused:
		hint = "the license was not used for the last " + strconv.Itoa(s.Policy.UnusedDays) + " days"
	case domain.EndExpired:
		hint = "the requested duration has elapsed"
	}
	now := s.Now()
	for _, a := range removed {
		_ = s.send(ctx, "removed", a.UserEmail, "Your Gemini Code Assist license was removed", removedMail{
			mailBase: s.base(a.UserEmail), Since: a.AssignedAt, Until: now, Reason: reason.Label(), ReasonHint: hint,
		})
	}
}

// RetryEmails re-sends the confirmation e-mails that failed.
func (s *Service) RetryEmails(ctx context.Context) (int, error) {
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
