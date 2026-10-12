package service

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"slices"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/gcp"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// unassignChunk is the number of usernames sent per unassign call.
const unassignChunk = 100

// =============================================================================
// Escalations
// =============================================================================

// Approve approves an escalated request and assigns the license right away
// (or queues it when the pool is exhausted).
func (s *Service) Approve(ctx context.Context, admin string, requestID int64) (*FulfilResult, error) {
	var req *store.Request
	err := s.DB.Tx(ctx, func(q store.Q) error {
		var err error
		if req, err = q.GetRequestForUpdate(ctx, requestID); errors.Is(err, store.ErrNotFound) {
			return notFound("Request not found.")
		} else if err != nil {
			return err
		}
		if req.Status != domain.ReqPendingAdmin {
			return conflict("This request is not waiting for a decision anymore.")
		}
		now := s.Now()
		req.Decision, req.DecidedAt, req.DecidedBy = ptr(domain.DecisionApproved), &now, &admin
		if err := q.UpdateRequest(ctx, req); err != nil {
			return err
		}
		return q.Audit(ctx, admin, "request.approve", req.UserEmail, map[string]any{"request": requestID})
	})
	if err != nil {
		return nil, err
	}
	jctx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
	defer cancel()
	return s.fulfil(jctx, req, domain.SourceAdmin, admin, domain.PriorityNormal)
}

// Reject rejects an escalated request. The rejection history is kept.
func (s *Service) Reject(ctx context.Context, admin string, requestID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return badRequest("A rejection reason is required.")
	}
	var req *store.Request
	err := s.DB.Tx(ctx, func(q store.Q) error {
		var err error
		if req, err = q.GetRequestForUpdate(ctx, requestID); errors.Is(err, store.ErrNotFound) {
			return notFound("Request not found.")
		} else if err != nil {
			return err
		}
		if req.Status != domain.ReqPendingAdmin {
			return conflict("This request is not waiting for a decision anymore.")
		}
		now := s.Now()
		req.Status = domain.ReqRejected
		req.Decision, req.DecidedAt, req.DecidedBy, req.DecisionReason = ptr(domain.DecisionRejected), &now, &admin, &reason
		req.CompletedAt = &now
		if err := q.UpdateRequest(ctx, req); err != nil {
			return err
		}
		return q.Audit(ctx, admin, "request.reject", req.UserEmail, map[string]any{"request": requestID, "reason": reason})
	})
	if err != nil {
		return err
	}
	_ = s.send(ctx, "rejected", req.UserEmail, "Your Gemini Code Assist request was rejected",
		rejectedMail{mailBase: s.base(req.UserEmail), DecidedAt: req.DecidedAt, Reason: reason})
	return nil
}

// =============================================================================
// Manual assignment, removal and transfer
// =============================================================================

// AdminAssign assigns a license to a user without the form (source ADMIN).
func (s *Service) AdminAssign(ctx context.Context, admin, email, duration string) (*FulfilResult, error) {
	email, err := validEmail(email)
	if err != nil {
		return nil, err
	}
	req, err := s.adminRequest(ctx, admin, email, duration)
	if err != nil {
		return nil, err
	}
	_ = s.DB.Audit(ctx, admin, "license.assign", email, map[string]any{"duration": duration})
	jctx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
	defer cancel()
	return s.fulfil(jctx, req, domain.SourceAdmin, admin, domain.PriorityNormal)
}

// adminRequest creates (or reuses) the request backing an admin assignment.
// A user already waiting (queue / pending admin) has their request reused.
func (s *Service) adminRequest(ctx context.Context, admin, email, duration string) (*store.Request, error) {
	dur, err := domain.ParseDuration(duration)
	if err != nil || !s.Policy.AllowsDuration(dur) {
		return nil, badRequest("Please select a valid duration.")
	}
	if snap, err := s.pool.Get(ctx); err == nil {
		if _, ok := snap.Has(email); ok {
			return nil, conflict("%s already holds a license.", email)
		}
	}
	now := s.Now()
	open, err := s.DB.OpenRequestFor(ctx, email)
	switch {
	case errors.Is(err, store.ErrNotFound):
		req := &store.Request{UserEmail: email, Status: domain.ReqDraft, Origin: "ADMIN", Duration: dur}
		if err := s.DB.CreateRequest(ctx, req); err != nil {
			return nil, err
		}
		req.Decision, req.DecidedAt, req.DecidedBy = ptr(domain.DecisionApproved), &now, &admin
		// Directory attributes are fetched for the inventory filters only (best effort).
		dctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindAdmin), 3*time.Second)
		if acc, err := s.Directory.Lookup(dctx, email); err == nil {
			s.applyDirectory(req, acc, domain.DirOK, "", now)
		}
		cancel()
		return req, s.DB.UpdateRequest(ctx, req)
	case err != nil:
		return nil, err
	case open.Status == domain.ReqBuffered:
		return nil, conflict("%s has a request in the buffer: use \"retry now\" in the buffer list instead.", email)
	}
	// Reuse the open request (queued, pending admin, draft, directory KO).
	// A queued request stays queued: if no seat is free its position is kept.
	open.Duration = dur
	open.Decision, open.DecidedAt, open.DecidedBy = ptr(domain.DecisionApproved), &now, &admin
	if open.Status != domain.ReqQueued {
		open.Status = domain.ReqDraft
	}
	return open, s.DB.UpdateRequest(ctx, open)
}

// UnassignResult reports a removal.
type UnassignResult struct {
	Removed []string          `json:"removed"`
	Failed  map[string]string `json:"failed"`
}

// Unassign removes licenses (admin removal) and serves the queue.
func (s *Service) Unassign(ctx context.Context, admin string, emails []string) (*UnassignResult, error) {
	var clean []string
	for _, e := range emails {
		if e = domain.NormalizeEmail(e); e != "" {
			clean = append(clean, e)
		}
	}
	slices.Sort(clean)
	clean = slices.Compact(clean)
	if len(clean) == 0 {
		return nil, badRequest("No user selected.")
	}
	actx := telemetry.WithKind(ctx, telemetry.KindAdmin)
	removed, failed, err := s.unassign(actx, clean, domain.EndAdminRemoval, admin)
	if err != nil {
		return nil, err
	}
	_ = s.DB.Audit(ctx, admin, "license.unassign", fmt.Sprintf("%d users", len(clean)), map[string]any{"emails": clean, "failed": failed})
	s.notifyRemoved(ctx, removed, domain.EndAdminRemoval)
	s.kickQueue()
	res := &UnassignResult{Failed: failed}
	for _, e := range clean {
		if _, ko := failed[e]; !ko {
			res.Removed = append(res.Removed, e)
		}
	}
	return res, nil
}

// UnassignByFilter removes every license matching inventory criteria. The
// caller passes the count it previewed: if the selection changed meanwhile
// the operation is refused (protection against surprises in mass removal).
func (s *Service) UnassignByFilter(ctx context.Context, admin string, f store.InventoryFilter, expected int) (*UnassignResult, error) {
	emails, err := s.DB.InventoryEmails(ctx, f, s.Now())
	if err != nil {
		return nil, err
	}
	if len(emails) != expected {
		return nil, conflict("The selection changed (%d users now match instead of %d). Please review it again.", len(emails), expected)
	}
	_ = s.DB.Audit(ctx, admin, "license.mass_unassign", fmt.Sprintf("%d users", len(emails)), map[string]any{"filter": f})
	return s.Unassign(ctx, admin, emails)
}

// Transfer removes the license of one user and gives it to another one in a
// single critical section, so the queue cannot take the released seat.
func (s *Service) Transfer(ctx context.Context, admin, from, to, duration string) (*FulfilResult, error) {
	from, err := validEmail(from)
	if err != nil {
		return nil, err
	}
	if to, err = validEmail(to); err != nil {
		return nil, err
	}
	if from == to {
		return nil, badRequest("Source and target users must be different.")
	}
	if !s.holdsLicense(ctx, from) {
		return nil, badRequest("%s does not hold a license.", from)
	}
	req, err := s.adminRequest(ctx, admin, to, duration)
	if err != nil {
		return nil, err
	}
	actx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
	defer cancel()
	// 1. Reserve a seat for the target first: the seat released by `from` is
	//    then invisible to the queue (available - reserved stays at zero).
	if req.ReservedAt == nil {
		if err := s.DB.WithLock(actx, store.LockPool, func() error { return s.holdSeat(actx, req) }); err != nil {
			return nil, err
		}
	}
	// 2. Release the source license.
	removed, failed, err := s.unassign(actx, []string{from}, domain.EndAdminRemoval, admin)
	if err == nil && failed[from] != "" {
		err = conflict("Cannot remove the license of %s: %s", from, failed[from])
	}
	if err != nil {
		req.ReservedAt = nil
		_ = s.DB.UpdateRequest(context.WithoutCancel(ctx), req)
		return nil, err
	}
	// 3. Assign the target (its reservation skips the availability check).
	res, err := s.fulfilSteps(actx, req, domain.SourceAdmin, admin, domain.PriorityNormal)
	if err != nil {
		return nil, err
	}
	_ = s.DB.Audit(ctx, admin, "license.transfer", from+" -> "+to, map[string]any{"duration": duration, "outcome": res.Outcome})
	s.notifyRemoved(ctx, removed, domain.EndAdminRemoval)
	s.notifyOutcome(ctx, req, res)
	return res, nil
}

// unassign removes licenses in Google, revokes credentials and closes the
// assignments in the database. Releasing seats needs no lock: only seat
// consumption is serialised (see reserveSeat).
func (s *Service) unassign(ctx context.Context, emails []string, reason domain.EndReason, actor string) ([]store.Assignment, map[string]string, error) {
	failed := map[string]string{}
	cur, err := s.DB.CurrentSubscription(ctx)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, nil, err
	}
	// Group the users by the subscription holding their license.
	bySub := map[string][]string{}
	active := map[string]store.Assignment{}
	for _, e := range emails {
		sub := ""
		if cur != nil {
			sub = cur.ID
		}
		if a, err := s.DB.ActiveAssignment(ctx, e); err == nil {
			active[e] = *a
			sub = a.SubscriptionID
		}
		if sub == "" {
			failed[e] = "no subscription"
			continue
		}
		bySub[sub] = append(bySub[sub], e)
	}

	var ok []string
	for sub, list := range bySub {
		for chunk := range slices.Chunk(list, unassignChunk) {
			if err := s.Licensing.Unassign(ctx, sub, chunk); err != nil {
				for _, e := range chunk {
					failed[e] = telemetry.Classify(err) + ": " + err.Error()
				}
				continue
			}
			ok = append(ok, chunk...)
		}
	}
	s.pool.Invalidate()
	if len(ok) == 0 {
		return nil, failed, nil
	}

	// Revoke the IAM bundle (best effort, bounded parallelism).
	g, gctx := errgroup.WithContext(context.WithoutCancel(ctx))
	g.SetLimit(8)
	for _, e := range ok {
		g.Go(func() error {
			rctx, cancel := context.WithTimeout(gctx, 10*time.Second)
			defer cancel()
			_ = s.Credentials.Revoke(rctx, e) // failures are visible in api_calls
			return nil
		})
	}
	_ = g.Wait()

	now := s.Now()
	wctx, cancel := detached(ctx, 10*time.Second)
	defer cancel()
	if _, err := s.DB.EndActiveAssignmentsByEmail(wctx, ok, reason, actor, now); err != nil {
		return nil, failed, err
	}
	var removed []store.Assignment
	for _, e := range ok {
		if a, found := active[e]; found {
			removed = append(removed, a)
		}
	}
	return removed, failed, nil
}

// holdsLicense reports whether a user holds a license (Google or database).
func (s *Service) holdsLicense(ctx context.Context, email string) bool {
	if snap, err := s.pool.Get(ctx); err == nil {
		if _, ok := snap.Has(email); ok {
			return true
		}
	}
	_, err := s.DB.ActiveAssignment(ctx, email)
	return err == nil
}

// CancelQueueEntry removes a user from the waiting list (admin action).
func (s *Service) CancelQueueEntry(ctx context.Context, admin string, entryID int64) error {
	return s.DB.Tx(ctx, func(q store.Q) error {
		e, err := q.GetQueueEntry(ctx, entryID)
		if errors.Is(err, store.ErrNotFound) {
			return notFound("Queue entry not found.")
		}
		if err != nil {
			return err
		}
		if e.Status != domain.QueueWaiting {
			return conflict("This entry is not waiting anymore.")
		}
		req, err := q.GetRequestForUpdate(ctx, e.RequestID)
		if err != nil {
			return err
		}
		now := s.Now()
		if err := q.CloseQueueEntries(ctx, e.RequestID, domain.QueueCancelled, now); err != nil {
			return err
		}
		req.Status, req.CompletedAt = domain.ReqAbandoned, &now
		req.FailureReason = ptr("Removed from the waiting list by an administrator")
		if err := q.UpdateRequest(ctx, req); err != nil {
			return err
		}
		return q.Audit(ctx, admin, "queue.cancel", e.UserEmail, map[string]any{"entry": entryID})
	})
}

// =============================================================================
// Deviations
// =============================================================================

// Deviation actions offered by the UI.
const (
	DevActionRemove   = "remove"   // GOOGLE_ONLY: unassign in Google
	DevActionBackport = "backport" // GOOGLE_ONLY: create the database record
	DevActionClose    = "close"    // DB_ONLY: close the database record
	DevActionReassign = "reassign" // DB_ONLY: assign again in Google
)

// ResolveDeviation applies the action chosen by an administrator. Google is
// the source of truth: "remove" and "close" align the portal on Google,
// "backport" adopts what Google has, "reassign" pushes the portal's view.
func (s *Service) ResolveDeviation(ctx context.Context, admin string, id int64, action string) error {
	d, err := s.DB.GetDeviation(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return notFound("Deviation not found.")
	}
	if err != nil {
		return err
	}
	if d.ResolvedAt != nil {
		return conflict("This deviation is already resolved.")
	}
	actx, cancel := s.jobCtx(ctx, telemetry.KindAdmin)
	defer cancel()
	now := s.Now()
	var res domain.DeviationResolution
	err = func() error {
		switch {
		case d.Kind == domain.DeviationGoogleOnly && action == DevActionRemove:
			res = domain.ResolveRemoveFromGoogle
			if err := s.Licensing.Unassign(actx, d.SubscriptionID, []string{d.UserEmail}); err != nil {
				return err
			}
			_ = s.Credentials.Revoke(actx, d.UserEmail)
		case d.Kind == domain.DeviationGoogleOnly && action == DevActionBackport:
			res = domain.ResolveBackport
			users, err := s.Licensing.Enumerate(actx, d.SubscriptionID)
			if err != nil {
				return err
			}
			var gu *gcp.LicensedUser
			for i := range users {
				if users[i].Email == d.UserEmail {
					gu = &users[i]
				}
			}
			if gu == nil {
				return conflict("%s is not licensed in Google anymore.", d.UserEmail)
			}
			a := &store.Assignment{UserEmail: d.UserEmail, SubscriptionID: d.SubscriptionID, Source: domain.SourceReconciled,
				AssignedBy: &admin, Duration: s.Policy.DefaultDuration, AssignedAt: gu.AssignTime,
				ExpiresAt: s.Policy.DefaultDuration.AddTo(now), LastUsageAt: gu.RecentUsage}
			if err := s.DB.InsertAssignment(actx, a); err != nil {
				if store.IsUniqueViolation(err) {
					return conflict("%s already has an active record.", d.UserEmail)
				}
				return err
			}
		case d.Kind == domain.DeviationDBOnly && action == DevActionClose:
			res = domain.ResolveCloseDB
			if _, err := s.DB.EndActiveAssignmentsByEmail(actx, []string{d.UserEmail}, domain.EndExternalRemoval, admin, now); err != nil {
				return err
			}
		case d.Kind == domain.DeviationDBOnly && action == DevActionReassign:
			res = domain.ResolveReassign
			if err := s.Licensing.Assign(actx, d.SubscriptionID, []string{d.UserEmail}); err != nil {
				return err
			}
		default:
			return badRequest("Action %q is not valid for a %s deviation.", action, d.Kind)
		}
		s.pool.Invalidate()
		return s.DB.ResolveDeviation(actx, id, res, admin, now)
	}()
	if err != nil {
		return err
	}
	_ = s.DB.Audit(ctx, admin, "deviation.resolve", d.UserEmail, map[string]any{"deviation": id, "action": action})
	if res == domain.ResolveRemoveFromGoogle || res == domain.ResolveCloseDB {
		s.kickQueue()
	}
	return nil
}

// =============================================================================
// Read models for the admin pages
// =============================================================================

// AdminOverview is the content of the admin page.
type AdminOverview struct {
	Pending   []store.Request
	Queue     []store.QueueEntry
	Buffer    []store.BufferEntry
	Abandoned []store.BufferEntry
	Decisions []store.Request
	Audit     []store.AuditEntry
	Counter   PoolCounter
}

// Overview loads the admin page.
func (s *Service) Overview(ctx context.Context) (*AdminOverview, error) {
	o := &AdminOverview{Counter: s.Counter(ctx)}
	var err error
	if o.Pending, err = s.DB.PendingAdminRequests(ctx); err != nil {
		return nil, err
	}
	if o.Queue, err = s.DB.ListQueue(ctx); err != nil {
		return nil, err
	}
	if o.Buffer, err = s.DB.ListPendingBuffer(ctx); err != nil {
		return nil, err
	}
	if o.Abandoned, err = s.DB.ListAbandonedBuffer(ctx, 20); err != nil {
		return nil, err
	}
	if o.Decisions, err = s.DB.RecentDecisions(ctx, 20); err != nil {
		return nil, err
	}
	if o.Audit, err = s.DB.ListAudit(ctx, 50); err != nil {
		return nil, err
	}
	return o, nil
}

// PendingCount returns the number of escalations waiting (top bar badge).
func (s *Service) PendingCount(ctx context.Context) int {
	n, _ := s.DB.CountByStatus(ctx, domain.ReqPendingAdmin)
	return n
}

// InventoryRow is one line of the license inventory.
type InventoryRow struct {
	store.Assignment
	GoogleLastUsage *time.Time `json:"googleLastUsage"`
	InGoogle        bool       `json:"inGoogle"`
	UnusedDays      int        `json:"unusedDays"`
}

// InventoryView is the inventory with its facets.
type InventoryView struct {
	Rows   []InventoryRow `json:"rows"`
	Total  int            `json:"total"`
	Facets store.Facets   `json:"facets"`
}

// Inventory returns the active assignments matching a filter, enriched with
// what Google reports (source of truth).
func (s *Service) Inventory(ctx context.Context, f store.InventoryFilter) (*InventoryView, error) {
	now := s.Now()
	rows, total, err := s.DB.Inventory(ctx, f, now, 1000)
	if err != nil {
		return nil, err
	}
	facets, err := s.DB.InventoryFacets(ctx)
	if err != nil {
		return nil, err
	}
	snap, _ := s.pool.Get(ctx)
	v := &InventoryView{Total: total, Facets: facets, Rows: make([]InventoryRow, 0, len(rows))}
	for _, a := range rows {
		r := InventoryRow{Assignment: a}
		if gu, ok := snap.Has(a.UserEmail); ok {
			r.InGoogle = true
			r.GoogleLastUsage = gu.RecentUsage
		}
		last := a.AssignedAt
		for _, t := range []*time.Time{a.LastUsageAt, r.GoogleLastUsage, a.MigratedAt} {
			if t != nil && t.After(last) {
				last = *t
			}
		}
		r.UnusedDays = int(now.Sub(last).Hours() / 24)
		v.Rows = append(v.Rows, r)
	}
	return v, nil
}

// validEmail normalises and validates an e-mail address.
func validEmail(s string) (string, error) {
	s = domain.NormalizeEmail(s)
	if a, err := mail.ParseAddress(s); err != nil || a.Address != s || !strings.Contains(s, ".") {
		return "", badRequest("%q is not a valid e-mail address.", s)
	}
	return s, nil
}
