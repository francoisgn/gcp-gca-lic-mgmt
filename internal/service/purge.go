package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
)

// Purge is the "purge" job (hourly):
//   - licenses whose requested duration elapsed are removed (EXPIRED);
//   - licenses without usage for policy.unused_days (7) are removed (UNUSED).
//
// The "last activity" of a license is the most recent of: assignment date,
// subscription rollover date, last usage stored in the database and last
// usage reported by Google (recentUsageTime, refreshed by Google within 24h).
// Released seats are given to the waiting list.
func (s *Service) Purge(ctx context.Context) (string, error) {
	cur, err := s.DB.CurrentSubscription(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return "no subscription yet", nil
	}
	if err != nil {
		return "", err
	}
	var expired, unused []store.Assignment
	var failed int
	err = func() error {
		users, err := s.Licensing.Enumerate(ctx, cur.ID)
		if err != nil {
			return err
		}
		usage := map[string]*time.Time{}
		for _, u := range users {
			usage[u.Email] = u.RecentUsage
		}
		active, err := s.DB.AllActiveAssignments(ctx)
		if err != nil {
			return err
		}
		now := s.Now()
		threshold := time.Duration(s.Policy.UnusedDays) * 24 * time.Hour
		for _, a := range active {
			if !a.ExpiresAt.After(now) {
				expired = append(expired, a)
				continue
			}
			last := a.AssignedAt
			for _, t := range []*time.Time{a.MigratedAt, a.LastUsageAt, usage[a.UserEmail]} {
				if t != nil && t.After(last) {
					last = *t
				}
			}
			if now.Sub(last) >= threshold {
				unused = append(unused, a)
			}
		}
		for _, batch := range []struct {
			list   *[]store.Assignment
			reason domain.EndReason
		}{{&expired, domain.EndExpired}, {&unused, domain.EndUnused}} {
			if len(*batch.list) == 0 {
				continue
			}
			emails := make([]string, len(*batch.list))
			for i, a := range *batch.list {
				emails[i] = a.UserEmail
			}
			removed, ko, err := s.unassign(ctx, emails, batch.reason, "system")
			if err != nil {
				return err
			}
			failed += len(ko)
			*batch.list = removed
		}
		return nil
	}()
	if err != nil {
		return "", err
	}
	s.notifyRemoved(ctx, expired, domain.EndExpired)
	s.notifyRemoved(ctx, unused, domain.EndUnused)
	if len(expired)+len(unused) > 0 {
		s.kickQueue()
	}
	return fmt.Sprintf("expired=%d unused=%d failed=%d", len(expired), len(unused), failed), nil
}

// Housekeeping is the daily cleanup job: technical KPIs retention (10 days),
// job history (30 days) and notification journal (90 days).
func (s *Service) Housekeeping(ctx context.Context) (string, error) {
	now := s.Now()
	calls, err := s.DB.PurgeAPICalls(ctx, now.Add(-s.Cfg.MetricsRetention))
	if err != nil {
		return "", err
	}
	runs, err := s.DB.PurgeJobRuns(ctx, now.AddDate(0, 0, -30))
	if err != nil {
		return "", err
	}
	notifs, err := s.DB.PurgeNotifications(ctx, now.AddDate(0, 0, -90))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("api_calls_deleted=%d job_runs_deleted=%d notifications_deleted=%d", calls, runs, notifs), nil
}
