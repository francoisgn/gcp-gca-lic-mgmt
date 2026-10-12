package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// RefreshSubscriptions is the "subscription-refresh" job (every 3 hours and on
// demand by a super admin):
//
//  1. list the Gemini Code Assist orders of the billing account;
//  2. upsert them (state, dates, seats);
//  3. switch to the newest active subscription: each new subscription makes
//     the previous one inoperative (even if Google still shows it active), so
//     as soon as a more recent one appears — or the current one becomes
//     inactive — every active license is migrated to the new pool;
//  4. reconcile the current pool with the database (deviations, last usage)
//     and the IAM bundle group with the licenses;
//  5. store the daily seat counters and serve the queue.
func (s *Service) RefreshSubscriptions(ctx context.Context) (string, error) {
	now := s.Now()
	orders, err := s.Licensing.ListOrders(ctx)
	if err != nil {
		return "", fmt.Errorf("list orders: %w", err)
	}
	var seen []string
	var candidates []store.Subscription
	for _, o := range orders {
		if len(s.Cfg.OrderIDs) == 0 && !s.Cfg.OrderMatch.MatchString(o.Offer+" "+o.DisplayName) {
			continue // not a Gemini Code Assist order
		}
		sub := store.Subscription{ID: o.Name, DisplayName: o.DisplayName, Offer: o.Offer,
			StartTime: o.Start, EndTime: o.End, AutoRenew: o.AutoRenew, State: "INACTIVE"}
		if o.ActiveAt(now) {
			pool, err := s.Licensing.GetPool(ctx, o.Name)
			switch {
			case telemetry.Classify(err) == telemetry.OutcomeNotFound:
				continue // the order has no license pool
			case err != nil:
				return "", fmt.Errorf("get pool of %s: %w", o.Name, err)
			}
			sub.SeatsTotal, sub.SeatsAvailable = pool.Total, pool.Available
			if pool.Total > 0 {
				sub.State = "ACTIVE"
				candidates = append(candidates, sub)
			}
		}
		if err := s.DB.UpsertSubscription(ctx, sub, now); err != nil {
			return "", err
		}
		seen = append(seen, sub.ID)
	}
	if err := s.DB.MarkUnseenInactive(ctx, seen, now); err != nil {
		return "", err
	}

	summary := fmt.Sprintf("orders=%d gca=%d active=%d", len(orders), len(seen), len(candidates))
	current, err := s.DB.CurrentSubscription(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return "", err
	}

	// Rollover: pick the best active subscription (latest start, then largest
	// pool) and switch when the current one is gone or a newer one appeared.
	sort.SliceStable(candidates, func(i, j int) bool {
		si, sj := candidates[i].StartTime, candidates[j].StartTime
		if si != nil && sj != nil && !si.Equal(*sj) {
			return si.After(*sj)
		}
		return candidates[i].SeatsTotal > candidates[j].SeatsTotal
	})
	if current == nil || current.State != "ACTIVE" || (len(candidates) > 0 && newer(&candidates[0], current)) {
		if len(candidates) > 0 {
			next := candidates[0]
			prev := ""
			if current != nil {
				prev = current.ID
				moved, queued, err := s.migrate(ctx, current, &next)
				if err != nil {
					return "", fmt.Errorf("migrate %s -> %s: %w", current.ID, next.ID, err)
				}
				summary += fmt.Sprintf(" migrated=%d requeued=%d", moved, queued)
			}
			if err := s.DB.SetCurrentSubscription(ctx, next.ID, prev); err != nil {
				return "", err
			}
			_ = s.DB.Audit(ctx, "system", "subscription.switch", next.ID, map[string]any{"previous": prev})
			slog.Info("current subscription switched", "from", prev, "to", next.ID)
		} else if current != nil {
			slog.Error("no active Gemini Code Assist subscription", "last", current.ID)
			summary += " NO_ACTIVE_SUBSCRIPTION"
		}
	}

	if cur, err := s.DB.CurrentSubscription(ctx); err == nil && cur.State == "ACTIVE" {
		dev, err := s.reconcile(ctx, cur)
		if err != nil {
			return "", fmt.Errorf("reconcile: %w", err)
		}
		summary += " " + dev
	}
	s.pool.Invalidate()
	s.kickQueue()
	return summary, nil
}

// newer reports whether subscription a started after b (a different order).
func newer(a, b *store.Subscription) bool {
	return a.ID != b.ID && a.StartTime != nil && (b.StartTime == nil || a.StartTime.After(*b.StartTime))
}

// migrate moves the active licenses of the previous subscription to the new
// one. When the new pool is smaller, the most recent users keep their seat
// (order of last usage, never-used licenses last); the others lose it, are
// queued with the highest priority and receive an e-mail with their position.
// Idempotent: a partial failure is completed by the next refresh (the old
// subscription stays current until the switch succeeds).
func (s *Service) migrate(ctx context.Context, from, to *store.Subscription) (moved, queued int, err error) {
	var requeued []store.Assignment
	defer func() { s.notifyRemoved(ctx, requeued, domain.EndSubscriptionEnded) }()
	err = s.DB.WithLock(ctx, store.LockPool, func() error {
		active, err := s.DB.ActiveAssignments(ctx, from.ID)
		if err != nil {
			return err
		}
		// Freshest usage known: database or Google (best effort on the old pool).
		usage := map[string]time.Time{}
		if users, err := s.Licensing.Enumerate(ctx, from.ID); err == nil {
			for _, u := range users {
				if u.RecentUsage != nil {
					usage[u.Email] = *u.RecentUsage
				}
			}
		}
		for _, a := range active {
			if a.LastUsageAt != nil && a.LastUsageAt.After(usage[a.UserEmail]) {
				usage[a.UserEmail] = *a.LastUsageAt
			}
		}
		sort.SliceStable(active, func(i, j int) bool {
			ui, uj := usage[active[i].UserEmail], usage[active[j].UserEmail]
			if ui.IsZero() != uj.IsZero() {
				return !ui.IsZero() // used before never used
			}
			if !ui.Equal(uj) {
				return ui.After(uj) // most recent usage first
			}
			return active[i].AssignedAt.After(active[j].AssignedAt) // never used: newest first
		})
		pool, err := s.Licensing.GetPool(ctx, to.ID)
		if err != nil {
			return err
		}
		existing, err := s.Licensing.Enumerate(ctx, to.ID)
		if err != nil {
			return err
		}
		already := map[string]bool{}
		for _, u := range existing {
			already[u.Email] = true
		}
		avail := pool.Available
		var toAssign []store.Assignment
		var keep []store.Assignment
		var overflow []store.Assignment
		for _, a := range active { // most recent usage first
			switch {
			case already[a.UserEmail]:
				keep = append(keep, a)
			case avail > 0:
				toAssign = append(toAssign, a)
				avail--
			default:
				overflow = append(overflow, a)
			}
		}
		now := s.Now()
		for chunk := range slices.Chunk(toAssign, unassignChunk) {
			emails := make([]string, len(chunk))
			for i, a := range chunk {
				emails[i] = a.UserEmail
			}
			if err := s.Licensing.Assign(ctx, to.ID, emails); err != nil {
				return err
			}
			keep = append(keep, chunk...)
		}
		for _, a := range keep {
			if err := s.DB.MoveAssignment(ctx, a.ID, to.ID, now); err != nil {
				return err
			}
			moved++
		}
		for _, a := range overflow {
			if err := s.requeueAfterRollover(ctx, a, now); err != nil {
				return err
			}
			requeued = append(requeued, a)
			queued++
		}
		return s.DB.Audit(ctx, "system", "subscription.migrate", to.ID,
			map[string]any{"from": from.ID, "moved": moved, "requeued": queued})
	})
	return moved, queued, err
}

// requeueAfterRollover ends an assignment that did not fit in the new pool
// and puts its owner at the head of the waiting list.
func (s *Service) requeueAfterRollover(ctx context.Context, a store.Assignment, now time.Time) error {
	_ = s.Credentials.Revoke(ctx, a.UserEmail)
	return s.DB.Tx(ctx, func(q store.Q) error {
		if err := q.EndAssignment(ctx, a.ID, domain.EndSubscriptionEnded, "system", now); err != nil {
			return err
		}
		req := &store.Request{UserEmail: a.UserEmail, Status: domain.ReqQueued, Origin: "MIGRATION", Duration: a.Duration}
		if err := q.CreateRequest(ctx, req); err != nil {
			return err
		}
		req.QueuedAt = &now
		req.DirSite, req.DirBusinessUnit, req.DirGPSCode = a.Site, a.BusinessUnit, a.GPSCode
		if err := q.UpdateRequest(ctx, req); err != nil {
			return err
		}
		_, err := q.Enqueue(ctx, req.ID, a.UserEmail, domain.PriorityMigration, now)
		return err
	})
}

// reconcile compares Google (source of truth) with the database for the
// current subscription: opens / refreshes deviations, self-heals the ones that
// disappeared, records the last usage reported by Google and the daily seat
// counters. Users holding a seat reservation (assignment in flight) or a
// pending buffer entry are not reported as GOOGLE_ONLY: their database record
// is about to be written.
func (s *Service) reconcile(ctx context.Context, cur *store.Subscription) (string, error) {
	var summary string
	err := func() error {
		observedAt := s.Now()
		pool, err := s.Licensing.GetPool(ctx, cur.ID)
		if err != nil {
			return err
		}
		users, err := s.Licensing.Enumerate(ctx, cur.ID)
		if err != nil {
			return err
		}
		active, err := s.DB.ActiveAssignments(ctx, cur.ID)
		if err != nil {
			return err
		}
		buffered, err := s.DB.ListPendingBuffer(ctx)
		if err != nil {
			return err
		}
		inBuffer := map[string]bool{}
		for _, b := range buffered {
			inBuffer[b.UserEmail] = true
		}
		reserved, err := s.DB.ReservedEmails(ctx)
		if err != nil {
			return err
		}
		for _, e := range reserved {
			inBuffer[e] = true
		}
		google := make(map[string]gcp.LicensedUser, len(users))
		for _, u := range users {
			google[u.Email] = u
		}
		db := make(map[string]store.Assignment, len(active))
		for _, a := range active {
			db[a.UserEmail] = a
		}

		googleOnly, dbOnly := 0, 0
		for email := range google {
			if _, ok := db[email]; !ok && !inBuffer[email] { // a buffered retry will create the record
				if err := s.DB.UpsertDeviation(ctx, email, cur.ID, domain.DeviationGoogleOnly, observedAt); err != nil {
					return err
				}
				googleOnly++
			}
		}
		for email, a := range db {
			gu, ok := google[email]
			if !ok {
				if err := s.DB.UpsertDeviation(ctx, email, cur.ID, domain.DeviationDBOnly, observedAt); err != nil {
					return err
				}
				dbOnly++
				continue
			}
			if gu.RecentUsage != nil && (a.LastUsageAt == nil || gu.RecentUsage.After(*a.LastUsageAt)) {
				if err := s.DB.SetLastUsage(ctx, a.ID, gu.RecentUsage); err != nil {
					return err
				}
			}
		}
		healed, err := s.DB.SelfHealDeviations(ctx, cur.ID, observedAt)
		if err != nil {
			return err
		}
		if err := s.DB.UpdateSeats(ctx, cur.ID, pool.Total, pool.Available); err != nil {
			return err
		}
		if err := s.DB.UpsertSeatStats(ctx, observedAt.In(s.Cfg.TimeZone), pool.Total, pool.Assigned()); err != nil {
			return err
		}
		summary = fmt.Sprintf("google=%d db=%d google_only=%d db_only=%d self_healed=%d %s", len(google), len(db), googleOnly, dbOnly, healed,
			s.reconcileCredentials(ctx, google, db, inBuffer))
		return nil
	}()
	return summary, err
}

// maxIAMChanges bounds the group changes of one reconciliation; the rest is
// done by the next refresh (3 hours later).
const maxIAMChanges = 200

// reconcileCredentials aligns the IAM bundle group with the licenses, so the
// Gemini roles never accumulate:
//
//   - a member with no license — not active in the portal, not assigned in
//     Google, not in flight (seat reserved / buffered) — loses the roles
//     (failed revocation, member added by hand in the console...);
//   - a user licensed both in the portal and in Google but missing from the
//     group gets the roles back.
//
// Google is part of the reference, so a database problem can never trigger a
// mass revocation. Owners and managers of the group are never listed, hence
// never touched. Best effort: failures are visible in the technical KPIs and
// in the job summary, they never fail the refresh.
func (s *Service) reconcileCredentials(ctx context.Context, google map[string]gcp.LicensedUser, db map[string]store.Assignment, inFlight map[string]bool) string {
	members, err := s.Credentials.Members(ctx)
	if errors.Is(err, gcp.ErrMembersUnsupported) {
		return "iam=unmanaged"
	}
	if err != nil {
		slog.Error("list IAM bundle members", "err", err)
		return "iam=error"
	}
	isMember := make(map[string]bool, len(members))
	var revoke, grant []string
	for _, m := range members {
		isMember[m] = true
		_, inDB := db[m]
		_, inGoogle := google[m]
		if !inDB && !inGoogle && !inFlight[m] {
			revoke = append(revoke, m)
		}
	}
	for email := range db {
		if _, inGoogle := google[email]; inGoogle && !isMember[email] {
			grant = append(grant, email)
		}
	}
	slices.Sort(revoke)
	slices.Sort(grant)
	revoke, grant = revoke[:min(len(revoke), maxIAMChanges)], grant[:min(len(grant), maxIAMChanges)]

	call := func(f func(context.Context, string) error, email string) bool {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		return f(cctx, email) == nil
	}
	var revoked, granted []string
	for _, e := range revoke {
		if call(s.Credentials.Revoke, e) {
			revoked = append(revoked, e)
		}
	}
	for _, e := range grant {
		if call(s.Credentials.Grant, e) {
			granted = append(granted, e)
		}
	}
	if len(revoked)+len(granted) > 0 {
		_ = s.DB.Audit(ctx, "system", "credentials.reconcile", "IAM bundle group",
			map[string]any{"revoked": revoked, "granted": granted})
		slog.Info("IAM bundle reconciled", "revoked", len(revoked), "granted", len(granted))
	}
	return fmt.Sprintf("iam_members=%d iam_revoked=%d iam_granted=%d", len(members), len(revoked), len(granted))
}
