package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"

	"golang.org/x/sync/errgroup"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Back-port: adopt a license that exists in Google but not in the database
// (GOOGLE_ONLY deviation) — typically a license assigned by the previous tool
// or directly in the console. Google is the source of truth, so the license is
// kept; the portal only takes over its management:
//
//   - the enterprise directory is checked first. A technical failure stops
//     the back-port (the deviation stays open, try again later); an unknown or
//     non-eligible user is back-ported and flagged "not eligible" in the
//     inventory: administrators decide;
//   - Google's assignment time and last usage are kept: the 7-day usage rule
//     applies at once, without exception; expiry = default duration from the
//     back-port;
//   - the user joins the IAM bundle group (best effort, the 3-hourly
//     reconciliation catches up); no e-mail is sent.

// BackportBatch is the number of deviations one "Back-port all" handles.
const BackportBatch = 200

// BackportUnavailable is returned when the directory does not answer.
const BackportUnavailable = "The enterprise directory is unavailable: nothing was back-ported. Try again in a few minutes."

// errGone: the user is not licensed in Google anymore (the next refresh
// closes the deviation as self-healed).
var errGone = errors.New("not licensed in Google anymore")

// backportOne creates the database record of a Google license after a
// directory check, and resolves the deviation. flagged = back-ported but not
// eligible today (or unknown in the directory).
func (s *Service) backportOne(ctx context.Context, admin string, d store.Deviation, gu gcp.LicensedUser) (flagged bool, err error) {
	lctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindAdmin), 2*s.Cfg.DirectoryTimeout)
	acc, lerr := s.Directory.Lookup(lctx, d.UserEmail)
	cancel()
	var check store.DirectoryCheck
	switch {
	case directory.IsNotFound(lerr):
		check = store.DirectoryCheck{Result: CheckIneligible, IneligibleReason: "Account not found in the enterprise directory."}
	case lerr != nil:
		slog.Warn("back-port stopped: enterprise directory unavailable", "email", d.UserEmail, "err", lerr)
		return false, &UserError{Status: http.StatusServiceUnavailable, Msg: BackportUnavailable}
	default:
		check = store.DirectoryCheck{Result: CheckOK, Status: acc.Status, Site: acc.Site, BusinessUnit: acc.BusinessUnit, GPSCode: acc.GPSCode}
		ok, reason, err := s.eligible(ctx, d.UserEmail, acc)
		if err != nil {
			return false, err
		}
		if !ok {
			check.Result, check.IneligibleReason = CheckIneligible, reason
		}
	}

	now := s.Now()
	assignedAt := gu.AssignTime
	if assignedAt.IsZero() || assignedAt.After(now) {
		assignedAt = now
	}
	a := &store.Assignment{UserEmail: d.UserEmail, SubscriptionID: d.SubscriptionID, Source: domain.SourceReconciled,
		AssignedBy: &admin, Duration: s.Policy.DefaultDuration, AssignedAt: assignedAt,
		ExpiresAt: s.Policy.DefaultDuration.AddTo(now), LastUsageAt: gu.RecentUsage, BackportedAt: &now,
		Site: nonEmpty(check.Site), BusinessUnit: nonEmpty(check.BusinessUnit), GPSCode: nonEmpty(check.GPSCode)}
	err = s.DB.Tx(ctx, func(q store.Q) error {
		if err := q.InsertAssignment(ctx, a); err != nil {
			if store.IsUniqueViolation(err) {
				return conflict("%s already has an active record.", d.UserEmail)
			}
			return err
		}
		if err := q.SetDirectoryCheck(ctx, a.ID, check, now); err != nil {
			return err
		}
		return q.ResolveDeviation(ctx, d.ID, domain.ResolveBackport, admin, now)
	})
	if err != nil {
		return false, err
	}
	cctx, ccancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindAdmin), s.Cfg.JobCallTimeout)
	if gerr := s.Credentials.Grant(cctx, d.UserEmail); gerr != nil {
		slog.Warn("back-port: IAM bundle grant failed (reconciliation will retry)", "email", d.UserEmail, "err", gerr)
	}
	ccancel()
	_ = s.DB.Audit(ctx, admin, "deviation.backport", d.UserEmail, map[string]any{
		"deviation": d.ID, "directory": check.Result, "reason": check.IneligibleReason})
	return check.Result == CheckIneligible, nil
}

// licensedUser finds a user in the Google list of a subscription.
func (s *Service) licensedUser(ctx context.Context, subscriptionID, email string) (gcp.LicensedUser, error) {
	actx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
	defer cancel()
	users, err := s.Licensing.Enumerate(actx, subscriptionID)
	if err != nil {
		return gcp.LicensedUser{}, err
	}
	for _, u := range users {
		if u.Email == email {
			return u, nil
		}
	}
	return gcp.LicensedUser{}, errGone
}

// BackportResult sums up a "Back-port all".
type BackportResult struct {
	Backported int `json:"backported"` // eligible today
	Flagged    int `json:"flagged"`    // back-ported, flagged not eligible / unknown in the directory
	Pending    int `json:"pending"`    // directory unavailable: still open, try again
	Skipped    int `json:"skipped"`    // not licensed in Google anymore / already in the portal
	Remaining  int `json:"remaining"`  // Google-only deviations still open
}

// BackportAll back-ports up to BackportBatch open GOOGLE_ONLY deviations
// (oldest first): one Google listing per subscription, directory calls in
// parallel (policy directory_recheck.concurrency). Once the directory fails,
// the rest of the batch is left pending without calling it again.
func (s *Service) BackportAll(ctx context.Context, admin string) (*BackportResult, error) {
	open, err := s.DB.OpenDeviations(ctx)
	if err != nil {
		return nil, err
	}
	var todo []store.Deviation
	for _, d := range open {
		if d.Kind == domain.DeviationGoogleOnly && len(todo) < BackportBatch {
			todo = append(todo, d)
		}
	}
	google := map[string]map[string]gcp.LicensedUser{} // subscription -> email -> user
	for _, d := range todo {
		if _, ok := google[d.SubscriptionID]; ok {
			continue
		}
		actx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
		users, err := s.Licensing.Enumerate(actx, d.SubscriptionID)
		cancel()
		if err != nil {
			return nil, err
		}
		byEmail := make(map[string]gcp.LicensedUser, len(users))
		for _, u := range users {
			byEmail[u.Email] = u
		}
		google[d.SubscriptionID] = byEmail
	}

	res := &BackportResult{}
	var directoryDown atomic.Bool
	var mu sync.Mutex
	count := func(n *int) { mu.Lock(); *n++; mu.Unlock() }
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(max(s.Policy.DirectoryRecheck.Concurrency, 1)) // gentle with the directory
	for _, d := range todo {
		g.Go(func() error {
			gu, ok := google[d.SubscriptionID][d.UserEmail]
			if !ok {
				count(&res.Skipped)
				return nil
			}
			if directoryDown.Load() {
				count(&res.Pending)
				return nil
			}
			flagged, err := s.backportOne(gctx, admin, d, gu)
			var ue *UserError
			switch {
			case errors.As(err, &ue) && ue.Status == http.StatusServiceUnavailable:
				directoryDown.Store(true)
				count(&res.Pending)
			case errors.As(err, &ue) && ue.Status == http.StatusConflict:
				count(&res.Skipped)
			case err != nil:
				return err // database error: stop
			case flagged:
				count(&res.Flagged)
			default:
				count(&res.Backported)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	googleOnly := 0
	for _, d := range open {
		if d.Kind == domain.DeviationGoogleOnly {
			googleOnly++
		}
	}
	res.Remaining = googleOnly - res.Backported - res.Flagged
	s.pool.Invalidate()
	_ = s.DB.Audit(ctx, admin, "deviation.backport_all", "", map[string]any{
		"backported": res.Backported, "flagged": res.Flagged, "pending": res.Pending, "skipped": res.Skipped})
	return res, nil
}

// nonEmpty returns nil for an empty string.
func nonEmpty(v string) *string {
	if v == "" {
		return nil
	}
	return &v
}
