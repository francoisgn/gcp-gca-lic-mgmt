package service_test

// Integration tests of the business rules (real PostgreSQL, fake Google and
// directory). Run with TEST_DATABASE_URL set, see internal/testenv.

import (
	"context"
	"testing"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/testenv"
)

var ctx = context.Background()

func mustOutcome(t *testing.T, res *service.FulfilResult, want service.FulfilOutcome) {
	t.Helper()
	if res == nil {
		t.Fatalf("no fulfilment result, want %s", want)
	}
	if res.Outcome != want {
		t.Fatalf("outcome = %s (%s), want %s", res.Outcome, res.Message, want)
	}
}

func activeCount(t *testing.T, e *testenv.Env) int {
	t.Helper()
	n, err := e.DB.CountActiveAssignments(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// Eligible user + free seat => license, credentials, e-mail, home page ACTIVE.
func TestAutomaticAssignment(t *testing.T) {
	e := testenv.New(t, 2)
	_, res := e.Request(t, "jane.doe@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)
	if !res.EmailSent || e.Mails.Count("jane.doe@example.com", "license is active") != 1 {
		t.Fatalf("confirmation e-mail not sent")
	}
	a, err := e.DB.ActiveAssignment(ctx, "jane.doe@example.com")
	if err != nil || a.Source != domain.SourceAuto || a.Site == nil {
		t.Fatalf("assignment = %+v, err = %v", a, err)
	}
	if want := domain.Duration("1M").AddTo(a.AssignedAt); !a.ExpiresAt.Equal(want) {
		t.Fatalf("expires at %v, want %v", a.ExpiresAt, want)
	}
	v, err := e.Svc.Home(ctx, e.User("jane.doe@example.com"), 1)
	if err != nil || v.State != service.StateActive {
		t.Fatalf("home state = %v, err = %v", v.State, err)
	}
	for _, s := range v.Steps {
		if s.ID != domain.StepAdmin && s.State != domain.StepOK {
			t.Fatalf("step %s = %s, want ok", s.ID, s.State)
		}
	}
	// A second request is refused.
	if _, err := e.Svc.CheckDirectory(ctx, e.User("jane.doe@example.com"), testenv.Form()); err == nil {
		t.Fatal("second request accepted while licensed")
	}
}

// Form validation is enforced server-side.
func TestFormValidation(t *testing.T) {
	e := testenv.New(t, 1)
	u := e.User("jane.doe@example.com")
	bad := []service.FormInput{
		{TrainingAck: false, Duration: "1M", Answers: testenv.Form().Answers},
		{TrainingAck: true, Duration: "2Y", Answers: testenv.Form().Answers},
		{TrainingAck: true, Duration: "1M", Answers: map[string]string{"use_case": "Dev", "justification": "short"}},
		{TrainingAck: true, Duration: "1M", Answers: map[string]string{"use_case": "Nope", "justification": "long enough text"}},
	}
	for i, in := range bad {
		if _, err := e.Svc.CheckDirectory(ctx, u, in); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

// Pool exhausted => queue with position; a released seat serves the queue head.
func TestQueue(t *testing.T) {
	e := testenv.New(t, 1)
	_, res := e.Request(t, "first.user@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)

	_, res = e.Request(t, "second.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)
	if res.QueuePosition != 1 {
		t.Fatalf("position = %d", res.QueuePosition)
	}
	_, res = e.Request(t, "third.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)
	if res.QueuePosition != 2 {
		t.Fatalf("position = %d", res.QueuePosition)
	}
	v, _ := e.Svc.Home(ctx, e.User("third.user@example.com"), 1)
	if v.State != service.StateQueued || v.Queue.Position != 2 || v.QueueTotal != 2 {
		t.Fatalf("home = %s %+v total=%d", v.State, v.Queue, v.QueueTotal)
	}
	// A newcomer sees the "pool exhausted" warning with its future position.
	v, _ = e.Svc.Home(ctx, e.User("newcomer@example.com"), 1)
	if !v.PoolExhausted || v.NextPosition != 3 {
		t.Fatalf("exhausted=%v next=%d", v.PoolExhausted, v.NextPosition)
	}

	if _, err := e.Svc.Unassign(ctx, "alex.martin@example.com", []string{"first.user@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.ProcessQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.ActiveAssignment(ctx, "second.user@example.com"); err != nil {
		t.Fatalf("queue head not served: %v", err)
	}
	if _, err := e.DB.ActiveAssignment(ctx, "third.user@example.com"); err == nil {
		t.Fatal("third user served without seat")
	}
	hist, _ := e.DB.AssignmentHistory(ctx, "first.user@example.com", 10)
	if len(hist) != 1 || *hist[0].EndReason != domain.EndAdminRemoval {
		t.Fatalf("history = %+v", hist)
	}
	// Leaving the queue.
	if err := e.Svc.LeaveQueue(ctx, e.User("third.user@example.com")); err != nil {
		t.Fatal(err)
	}
	if d, _ := e.DB.QueueDepth(ctx); d != 0 {
		t.Fatalf("queue depth = %d", d)
	}
}

// Functional KO => escalation => approval (source ADMIN) / rejection history.
func TestEscalation(t *testing.T) {
	e := testenv.New(t, 2)
	for _, email := range []string{"john.ko@example.com", "emma.nogps@example.com", "max.ghost@example.com"} {
		dir, _ := e.Request(t, email)
		if dir.Outcome != domain.DirFunctionalKO {
			t.Fatalf("%s: outcome %s", email, dir.Outcome)
		}
	}
	dir, _ := e.Request(t, "anna.ko@example.com")
	if _, err := e.Svc.Escalate(ctx, e.User("anna.ko@example.com"), dir.RequestID, service.EscalationInput{Division: "D"}); err == nil {
		t.Fatal("incomplete escalation accepted")
	}
	if _, err := e.Svc.Escalate(ctx, e.User("someone.else@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err == nil {
		t.Fatal("escalation of another user's request accepted")
	}
	if _, err := e.Svc.Escalate(ctx, e.User("anna.ko@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P", Comment: "please"}); err != nil {
		t.Fatal(err)
	}
	v, _ := e.Svc.Home(ctx, e.User("anna.ko@example.com"), 1)
	if v.State != service.StatePendingAdmin {
		t.Fatalf("state = %s", v.State)
	}

	// Rejection, then a new request approved.
	if err := e.Svc.Reject(ctx, "alex.martin@example.com", dir.RequestID, "Not eligible"); err != nil {
		t.Fatal(err)
	}
	v, _ = e.Svc.Home(ctx, e.User("anna.ko@example.com"), 1)
	if v.State != service.StateRejected || v.Rejections != 1 || len(v.Rejected) != 1 {
		t.Fatalf("after reject: state=%s rejections=%d", v.State, v.Rejections)
	}
	dir, _ = e.Request(t, "anna.ko@example.com")
	if _, err := e.Svc.Escalate(ctx, e.User("anna.ko@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.Approve(ctx, "alex.martin@example.com", dir.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	mustOutcome(t, res, service.OutcomeAssigned)
	a, _ := e.DB.ActiveAssignment(ctx, "anna.ko@example.com")
	if a.Source != domain.SourceAdmin {
		t.Fatalf("source = %s", a.Source)
	}
	v, _ = e.Svc.Home(ctx, e.User("anna.ko@example.com"), 1)
	for _, s := range v.Steps {
		if s.ID == domain.StepAdmin && s.State != domain.StepOK {
			t.Fatalf("admin step = %s, want ok after approval", s.State)
		}
	}
	if total, rejected, _ := e.DB.RequestCounts(ctx, "anna.ko@example.com"); total != 2 || rejected != 1 {
		t.Fatalf("counts = %d/%d", total, rejected)
	}
}

// Directory timeout => buffer (DIRECTORY stage, no seat reserved).
func TestDirectoryTimeoutBuffer(t *testing.T) {
	e := testenv.New(t, 1)
	dir, _ := e.Request(t, "lucas.edslow@example.com")
	if dir.Outcome != domain.DirTechnicalKO || dir.BufferPosition != 1 {
		t.Fatalf("outcome = %s pos=%d", dir.Outcome, dir.BufferPosition)
	}
	if n, _ := e.DB.ReservedSeats(ctx, 0); n != 0 {
		t.Fatalf("directory-buffered request reserves %d seat(s)", n)
	}
	v, _ := e.Svc.Home(ctx, e.User("lucas.edslow@example.com"), 1)
	if v.State != service.StateBuffered || v.Buffer.Stage != domain.StageDirectory {
		t.Fatalf("home = %s", v.State)
	}
	// Still hanging on retry: stays buffered, attempt counted.
	if _, err := e.Svc.RetryBuffer(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := e.DB.PendingBuffer(ctx, "lucas.edslow@example.com")
	if b == nil || b.Attempts != 1 {
		t.Fatalf("buffer = %+v", b)
	}
}

// Google timeout on assignment => buffer with a reserved seat that the queue
// cannot take; the retry job completes the assignment.
func TestGoogleTimeoutBufferAndReservation(t *testing.T) {
	e := testenv.New(t, 1)
	_, res := e.Request(t, "nina.slow@example.com") // first assign hangs
	mustOutcome(t, res, service.OutcomeBuffered)
	if res.BufferStage != domain.StageLicense {
		t.Fatalf("stage = %s", res.BufferStage)
	}
	if n, _ := e.DB.ReservedSeats(ctx, 0); n != 1 {
		t.Fatalf("reserved seats = %d, want 1", n)
	}
	// The single seat is reserved: a newcomer is queued, not served.
	_, res = e.Request(t, "late.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)

	if _, err := e.Svc.RetryBuffer(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.ActiveAssignment(ctx, "nina.slow@example.com"); err != nil {
		t.Fatalf("buffered request not completed: %v", err)
	}
	if n, _ := e.DB.ReservedSeats(ctx, 0); n != 0 {
		t.Fatalf("reservation not released: %d", n)
	}
	if d, _ := e.DB.BufferDepth(ctx); d != 0 {
		t.Fatalf("buffer depth = %d", d)
	}
}

// A slow Google call of one user must not block the others (no lock held
// during external calls).
func TestNoGlobalLockDuringGoogleCalls(t *testing.T) {
	e := testenv.New(t, 5)
	done := make(chan *service.FulfilResult, 1)
	go func() {
		_, res := e.Request(t, "nina.slow@example.com")
		done <- res
	}()
	time.Sleep(100 * time.Millisecond)
	start := time.Now()
	_, res := e.Request(t, "fast.user@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)
	if d := time.Since(start); d > 350*time.Millisecond {
		t.Fatalf("fast user waited %v behind the slow call", d)
	}
	mustOutcome(t, <-done, service.OutcomeBuffered)
}

// Purge: expired and unused (7 days) licenses are removed and notified.
func TestPurge(t *testing.T) {
	e := testenv.New(t, 5)
	e.Request(t, "active.user@example.com")
	e.Request(t, "lazy.idle@example.com") // never used (fake marker "idle")
	dir, err := e.Svc.CheckDirectory(ctx, e.User("short.user@example.com"),
		service.FormInput{TrainingAck: true, Duration: "7d", Answers: testenv.Form().Answers})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Submit(ctx, e.User("short.user@example.com"), dir.RequestID); err != nil {
		t.Fatal(err)
	}
	e.Clock.Advance(8 * 24 * time.Hour)
	// The active user keeps using the license.
	now := e.Clock.Now()
	if err := e.Fake.Seed("active.user@example.com", now.AddDate(0, 0, -8), &now); err != nil {
		t.Fatal(err)
	}
	summary, err := e.Svc.Purge(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(summary)
	check := func(email string, want *domain.EndReason) {
		t.Helper()
		_, err := e.DB.ActiveAssignment(ctx, email)
		if want == nil {
			if err != nil {
				t.Fatalf("%s removed", email)
			}
			return
		}
		h, _ := e.DB.AssignmentHistory(ctx, email, 1)
		if err == nil || len(h) != 1 || *h[0].EndReason != *want {
			t.Fatalf("%s: history %+v", email, h)
		}
	}
	check("active.user@example.com", nil)
	check("lazy.idle@example.com", ptr(domain.EndUnused))
	check("short.user@example.com", ptr(domain.EndExpired))
	if e.Mails.Count("lazy.idle@example.com", "removed") != 1 {
		t.Fatal("removal e-mail not sent")
	}
}

// Subscription rollover: licenses migrate; overflow goes first in the queue.
func TestSubscriptionRollover(t *testing.T) {
	e := testenv.New(t, 3)
	for _, u := range []string{"a.user@example.com", "b.user@example.com", "c.user@example.com"} {
		_, res := e.Request(t, u)
		mustOutcome(t, res, service.OutcomeAssigned)
	}
	_, res := e.Request(t, "waiting.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)

	if _, err := e.Fake.Rollover(2); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	cur, _ := e.DB.CurrentSubscription(ctx)
	if cur.SeatsTotal != 2 {
		t.Fatalf("current seats = %d", cur.SeatsTotal)
	}
	if n := activeCount(t, e); n != 2 {
		t.Fatalf("active after rollover = %d", n)
	}
	head, _ := e.DB.QueueHead(ctx)
	if head.UserEmail != "c.user@example.com" || head.Priority != domain.PriorityMigration {
		t.Fatalf("queue head = %+v (the overflow user must come first)", head)
	}
	users, _ := e.Fake.Enumerate(ctx, cur.ID)
	if len(users) != 2 {
		t.Fatalf("google users in new pool = %d", len(users))
	}
}

// Deviations: detection against Google and explicit resolution.
func TestDeviations(t *testing.T) {
	e := testenv.New(t, 5)
	e.Request(t, "known.user@example.com")
	e.Request(t, "vanished.user@example.com")
	if err := e.Fake.Seed("console.only@example.com", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	if err := e.Fake.Remove("vanished.user@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	devs, _ := e.DB.OpenDeviations(ctx)
	kinds := map[string]domain.DeviationKind{}
	ids := map[string]int64{}
	for _, d := range devs {
		kinds[d.UserEmail], ids[d.UserEmail] = d.Kind, d.ID
	}
	if kinds["console.only@example.com"] != domain.DeviationGoogleOnly || kinds["vanished.user@example.com"] != domain.DeviationDBOnly || len(devs) != 2 {
		t.Fatalf("deviations = %+v", kinds)
	}
	// A refresh does not duplicate them.
	if _, err := e.Svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if devs, _ = e.DB.OpenDeviations(ctx); len(devs) != 2 {
		t.Fatalf("deviations after 2nd refresh = %d", len(devs))
	}
	if err := e.Svc.ResolveDeviation(ctx, "super.admin@example.com", ids["console.only@example.com"], service.DevActionClose); err == nil {
		t.Fatal("invalid action accepted")
	}
	if err := e.Svc.ResolveDeviation(ctx, "super.admin@example.com", ids["console.only@example.com"], service.DevActionBackport); err != nil {
		t.Fatal(err)
	}
	if a, err := e.DB.ActiveAssignment(ctx, "console.only@example.com"); err != nil || a.Source != domain.SourceReconciled {
		t.Fatalf("backport: %+v %v", a, err)
	}
	if err := e.Svc.ResolveDeviation(ctx, "super.admin@example.com", ids["vanished.user@example.com"], service.DevActionClose); err != nil {
		t.Fatal(err)
	}
	if devs, _ = e.DB.OpenDeviations(ctx); len(devs) != 0 {
		t.Fatalf("open deviations = %d", len(devs))
	}
}

// Transfer is atomic: the queue cannot take the released seat.
func TestTransfer(t *testing.T) {
	e := testenv.New(t, 1)
	e.Request(t, "holder.user@example.com")
	_, res := e.Request(t, "queued.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)
	res, err := e.Svc.Transfer(ctx, "alex.martin@example.com", "holder.user@example.com", "target.user@example.com", "1M")
	if err != nil {
		t.Fatal(err)
	}
	mustOutcome(t, res, service.OutcomeAssigned)
	if _, err := e.Svc.ProcessQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := e.DB.ActiveAssignment(ctx, "queued.user@example.com"); err == nil {
		t.Fatal("queue took the transferred seat")
	}
	if _, err := e.Svc.Transfer(ctx, "alex.martin@example.com", "nobody@example.com", "x.user@example.com", "1M"); err == nil {
		t.Fatal("transfer from a user without license accepted")
	}
}

// Mass removal by criteria refuses a stale preview count.
func TestMassRemoval(t *testing.T) {
	e := testenv.New(t, 5)
	for _, u := range []string{"m1.user@example.com", "m2.user@example.com", "m3.user@example.com"} {
		e.Request(t, u)
	}
	f := store.InventoryFilter{Search: "m"}
	if _, err := e.Svc.UnassignByFilter(ctx, "alex.martin@example.com", f, 2); err == nil {
		t.Fatal("stale count accepted")
	}
	res, err := e.Svc.UnassignByFilter(ctx, "alex.martin@example.com", f, 3)
	if err != nil || len(res.Removed) != 3 {
		t.Fatalf("removed = %+v, err = %v", res, err)
	}
	if n := activeCount(t, e); n != 0 {
		t.Fatalf("active = %d", n)
	}
}

// Telemetry: every external call is recorded for the technical KPIs.
func TestTelemetryRecorded(t *testing.T) {
	e := testenv.New(t, 2)
	e.Request(t, "jane.doe@example.com")
	e.Request(t, "lucas.edslow@example.com")
	var n, timeouts int
	if err := e.DB.Pool.QueryRow(ctx, `SELECT count(*), count(*) FILTER (WHERE outcome='TIMEOUT' AND context='UX') FROM api_calls`).Scan(&n, &timeouts); err != nil {
		t.Fatal(err)
	}
	if n < 5 || timeouts != 1 {
		t.Fatalf("api_calls = %d, UX timeouts = %d", n, timeouts)
	}
	k, err := e.Svc.Tech(ctx)
	if err != nil || len(k.Days) != 10 {
		t.Fatalf("tech kpi: %v", err)
	}
	if _, err := e.Svc.Flows(ctx, 30); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.Depths(ctx, 30); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }
