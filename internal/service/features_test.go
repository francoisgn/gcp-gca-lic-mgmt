package service_test

// Integration tests of the v1.1 rules: external accounts, GPS allowlist,
// notifications, monthly directory re-check.

import (
	"strings"
	"testing"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/testenv"
)

func notifications(t *testing.T, e *testenv.Env, to string) []store.Notification {
	t.Helper()
	n, err := e.DB.ListNotifications(ctx, to, 100)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func events(list []store.Notification) []string {
	var out []string
	for i := len(list) - 1; i >= 0; i-- { // chronological
		out = append(out, list[i].Event)
	}
	return out
}

// External accounts (firstname.lastname.ext@) are never approved
// automatically, but an administrator can approve them.
func TestExternalAccounts(t *testing.T) {
	e := testenv.New(t, 3)
	dir, res := e.Request(t, "anna.dupont.ext@example.com")
	if res != nil || dir.Outcome != domain.DirFunctionalKO || dir.Reason != service.ExternalReason {
		t.Fatalf("external: outcome=%s reason=%q", dir.Outcome, dir.Reason)
	}
	if _, err := e.Svc.Escalate(ctx, e.User("anna.dupont.ext@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.Approve(ctx, "alex.martin@example.com", dir.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	mustOutcome(t, res, service.OutcomeAssigned)
	e.Request(t, "internal.user@example.com")

	inv, err := e.Svc.Inventory(ctx, store.InventoryFilter{AccountType: "external"})
	if err != nil || inv.Total != 1 || !inv.Rows[0].External {
		t.Fatalf("external filter: %+v %v", inv, err)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{AccountType: "internal"}); inv.Total != 1 {
		t.Fatalf("internal filter: total=%d", inv.Total)
	}
	sum, err := e.Svc.Summary(ctx)
	if err != nil || sum.Mix.External != 1 || sum.Mix.Active != 2 || sum.ExternalPct != 50 {
		t.Fatalf("mix = %+v pct=%v err=%v", sum.Mix, sum.ExternalPct, err)
	}
}

// Only GPS codes of the allowlist (database, managed by admins) are approved
// automatically.
func TestGPSAllowlist(t *testing.T) {
	e := testenv.New(t, 3)
	dir, _ := e.Request(t, "tom.badgps@example.com")
	if dir.Outcome != domain.DirFunctionalKO || !strings.Contains(dir.Reason, "not in the list") {
		t.Fatalf("outcome=%s reason=%q", dir.Outcome, dir.Reason)
	}
	if _, err := e.Svc.AddGPSCodes(ctx, "alex.martin@example.com", "zz-not-a-code", ""); err == nil {
		t.Fatal("invalid code accepted")
	}
	n, err := e.Svc.AddGPSCodes(ctx, "alex.martin@example.com", "0BADC0DE, a3f09c1e", "Team X")
	if err != nil || n != 1 { // a3f09c1e already present
		t.Fatalf("added=%d err=%v", n, err)
	}
	if err := e.Svc.ExitRequest(ctx, e.User("tom.badgps@example.com"), dir.RequestID); err != nil {
		t.Fatal(err)
	}
	_, res := e.Request(t, "tom.badgps@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)

	if err := e.Svc.RemoveGPSCode(ctx, "alex.martin@example.com", "0badc0de"); err != nil {
		t.Fatal(err)
	}
	if err := e.Svc.RemoveGPSCode(ctx, "alex.martin@example.com", "0badc0de"); err == nil {
		t.Fatal("removing an unknown code succeeded")
	}
}

// One e-mail per step change; admin approval = one e-mail when the license
// is assigned at once, two when the user has to wait.
func TestNotifications(t *testing.T) {
	e := testenv.New(t, 1)
	// Escalation: user + every admin (super admins excluded).
	dir, _ := e.Request(t, "anna.ko@example.com")
	if _, err := e.Svc.Escalate(ctx, e.User("anna.ko@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err != nil {
		t.Fatal(err)
	}
	if got := events(notifications(t, e, "anna.ko@example.com")); strings.Join(got, ",") != "request_escalated" {
		t.Fatalf("user events = %v", got)
	}
	for _, admin := range []string{"alex.martin@example.com", "sam.lee@example.com"} {
		if got := events(notifications(t, e, admin)); len(got) != 1 || got[0] != service.EvAdminEscalation {
			t.Fatalf("%s events = %v", admin, got)
		}
	}
	if len(notifications(t, e, "super.admin@example.com")) != 0 {
		t.Fatal("super admin notified of an escalation")
	}
	// Approval with a free seat: a single "license active" e-mail.
	if _, err := e.Svc.Approve(ctx, "alex.martin@example.com", dir.RequestID); err != nil {
		t.Fatal(err)
	}
	if got := events(notifications(t, e, "anna.ko@example.com")); strings.Join(got, ",") != "request_escalated,license_assigned" {
		t.Fatalf("after immediate approval: %v", got)
	}

	// Approval without seat: "approved (queued)" now, "license active" later.
	dir, _ = e.Request(t, "bob.ko@example.com")
	if _, err := e.Svc.Escalate(ctx, e.User("bob.ko@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.Approve(ctx, "alex.martin@example.com", dir.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	mustOutcome(t, res, service.OutcomeQueued)
	if _, err := e.Svc.Unassign(ctx, "alex.martin@example.com", []string{"anna.ko@example.com"}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Svc.ProcessQueue(ctx); err != nil {
		t.Fatal(err)
	}
	if got := events(notifications(t, e, "bob.ko@example.com")); strings.Join(got, ",") != "request_escalated,request_approved,license_assigned" {
		t.Fatalf("after delayed approval: %v", got)
	}
	if got := events(notifications(t, e, "anna.ko@example.com")); got[len(got)-1] != service.EvRemovedAdmin {
		t.Fatalf("manual removal not notified: %v", got)
	}

	// A disabled event is journaled but not sent.
	e.Svc.Policy.Notifications = map[string]bool{service.EvQueued: false}
	_, res = e.Request(t, "late.user@example.com")
	mustOutcome(t, res, service.OutcomeQueued)
	n := notifications(t, e, "late.user@example.com")
	if len(n) != 1 || n[0].Status != "DISABLED" || e.Mails.Count("late.user@example.com", "") != 0 {
		t.Fatalf("disabled event: %+v", n)
	}
}

// Monthly re-check: anniversary selection, drift, ineligibility, acknowledgement.
func TestDirectoryRecheck(t *testing.T) {
	e := testenv.New(t, 5)
	e.Request(t, "anniv.user@example.com")
	e.Request(t, "recent.user@example.com")
	e.Request(t, "checked.user@example.com")
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := e.DB.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	// Anniversary today (assigned 2 months ago, same day), recent license (10 days), checked 10 days ago.
	exec(`UPDATE license_assignments SET assigned_at = now() - interval '2 months' WHERE user_email = 'anniv.user@example.com'`)
	exec(`UPDATE license_assignments SET assigned_at = now() - interval '10 days' WHERE user_email = 'recent.user@example.com'`)
	exec(`UPDATE license_assignments SET assigned_at = now() - interval '2 months', dir_checked_at = now() - interval '10 days'
		WHERE user_email = 'checked.user@example.com'`)
	due, err := e.DB.DueForDirectoryCheck(ctx, "UTC", 100)
	if err != nil || len(due) != 1 || due[0].UserEmail != "anniv.user@example.com" {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	if _, err := e.Svc.RecheckDirectory(ctx); err != nil {
		t.Fatal(err)
	}
	a, _ := e.DB.ActiveAssignment(ctx, "anniv.user@example.com")
	if a.DirCheckResult == nil || *a.DirCheckResult != service.CheckOK || a.DirCheckedAt == nil {
		t.Fatalf("first check: %+v", a.DirCheckResult)
	}

	// The GPS code changed in the directory since the assignment.
	exec(`UPDATE license_assignments SET gps_code = 'ffffffff' WHERE user_email = 'anniv.user@example.com'`)
	if res, err := e.Svc.RecheckUser(ctx, "alex.martin@example.com", "anniv.user@example.com"); err != nil || res != service.CheckChanged {
		t.Fatalf("changed: %s %v", res, err)
	}
	inv, _ := e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"CHANGED"}})
	if inv.Total != 1 || inv.Rows[0].DriftFields[0] != "gps_code" {
		t.Fatalf("changed filter: %+v", inv.Rows)
	}
	// The administrator accepts the new values.
	if n, err := e.Svc.AcknowledgeDrift(ctx, "alex.martin@example.com", []string{"anniv.user@example.com"}); err != nil || n != 1 {
		t.Fatalf("acknowledge: %d %v", n, err)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"CHANGED"}}); inv.Total != 0 {
		t.Fatal("drift not cleared")
	}

	// The code is removed from the allowlist: no longer eligible.
	a, _ = e.DB.ActiveAssignment(ctx, "anniv.user@example.com")
	if err := e.Svc.RemoveGPSCode(ctx, "alex.martin@example.com", *a.GPSCode); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.Svc.RecheckUser(ctx, "alex.martin@example.com", "anniv.user@example.com"); res != service.CheckIneligible {
		t.Fatalf("ineligible: %s", res)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"INELIGIBLE"}}); inv.Total != 1 {
		t.Fatalf("ineligible filter: %d", inv.Total)
	}
	// Directory down: ERROR, retried the next night.
	exec(`UPDATE license_assignments SET user_email = 'anniv.edslow@example.com' WHERE user_email = 'anniv.user@example.com'`)
	if res, _ := e.Svc.RecheckUser(ctx, "alex.martin@example.com", "anniv.edslow@example.com"); res != service.CheckError {
		t.Fatalf("error: %s", res)
	}
}
