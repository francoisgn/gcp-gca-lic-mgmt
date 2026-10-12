package service

import (
	"context"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/tracker"
)

// SubmitFeedback stores a feedback and forwards it to the issue tracker. The
// requester is the SSO identity, never a value typed in the form.
func (s *Service) SubmitFeedback(ctx context.Context, user auth.Identity, category, message, page string) (*store.Feedback, error) {
	category = strings.ToUpper(strings.TrimSpace(category))
	switch category {
	case "IMPROVEMENT", "BUG", "QUESTION":
	default:
		return nil, badRequest("Please choose a category.")
	}
	message = strings.TrimSpace(message)
	if n := utf8.RuneCountInString(message); n < 10 || n > 4000 {
		return nil, badRequest("The message must contain between 10 and 4000 characters.")
	}
	f := &store.Feedback{UserEmail: user.Email, Category: category, Message: message}
	if err := s.DB.InsertFeedback(ctx, f); err != nil {
		return nil, err
	}
	tctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindUX), 10*time.Second)
	defer cancel()
	key, link, err := s.Tracker.Create(tctx, tracker.Issue{Category: category, Requester: user.Email, Message: message, PageURL: page})
	trackerErr := ""
	if err != nil {
		slog.Error("feedback forwarding failed", "err", err)
		trackerErr = "The issue tracker is unavailable, your feedback is saved and will be handled manually."
	}
	if err := s.DB.SetFeedbackTracker(ctx, f.ID, key, link, trackerErr); err != nil {
		return nil, err
	}
	if key != "" {
		f.TrackerKey = &key
	}
	if link != "" {
		f.TrackerURL = &link
	}
	if trackerErr != "" {
		f.TrackerError = &trackerErr
	}
	return f, nil
}

// MyFeedback lists the feedback of a user.
func (s *Service) MyFeedback(ctx context.Context, user auth.Identity) ([]store.Feedback, error) {
	return s.DB.ListFeedback(ctx, user.Email, 20)
}
