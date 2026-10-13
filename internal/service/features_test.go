package service_test

// Integration tests of the v1.1 rules: external accounts, GPS allowlist,
// notifications, monthly directory re-check.

import (
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

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

	inv, err := e.Svc.Inventory(ctx, store.InventoryFilter{AccountType: "external"}, 0)
	if err != nil || inv.Total != 1 || !inv.Rows[0].External {
		t.Fatalf("external filter: %+v %v", inv, err)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{AccountType: "internal"}, 0); inv.Total != 1 {
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
	inv, _ := e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"CHANGED"}}, 0)
	if inv.Total != 1 || inv.Rows[0].DriftFields[0] != "gps_code" {
		t.Fatalf("changed filter: %+v", inv.Rows)
	}
	// The administrator accepts the new values.
	if n, err := e.Svc.AcknowledgeDrift(ctx, "alex.martin@example.com", []string{"anniv.user@example.com"}); err != nil || n != 1 {
		t.Fatalf("acknowledge: %d %v", n, err)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"CHANGED"}}, 0); inv.Total != 0 {
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
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{DirectoryCheck: []string{"INELIGIBLE"}}, 0); inv.Total != 1 {
		t.Fatalf("ineligible filter: %d", inv.Total)
	}
	// Directory down: ERROR, retried the next night.
	exec(`UPDATE license_assignments SET user_email = 'anniv.edslow@example.com' WHERE user_email = 'anniv.user@example.com'`)
	if res, _ := e.Svc.RecheckUser(ctx, "alex.martin@example.com", "anniv.edslow@example.com"); res != service.CheckError {
		t.Fatalf("error: %s", res)
	}
}

// The IAM bundle group follows the licenses: every end of license revokes the
// roles, and the subscription refresh removes any member left without a
// license (failed revocation, member added by hand) and restores missing ones.
func TestCredentialsReconciliation(t *testing.T) {
	e := testenv.New(t, 3)
	_, res := e.Request(t, "anna.kept@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)
	_, res = e.Request(t, "ben.removed@example.com")
	mustOutcome(t, res, service.OutcomeAssigned)
	if m, _ := e.Creds.Members(ctx); !slices.Equal(m, []string{"anna.kept@example.com", "ben.removed@example.com"}) {
		t.Fatalf("members after assignment = %v", m)
	}

	// Removal by an administrator revokes the roles right away.
	if _, err := e.Svc.Unassign(ctx, "alex.martin@example.com", []string{"ben.removed@example.com"}); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.Creds.Members(ctx); !slices.Equal(m, []string{"anna.kept@example.com"}) {
		t.Fatalf("members after removal = %v", m)
	}

	// Drift: a stray member (failed revocation / added by hand) and a licensed
	// user who lost the roles. The refresh fixes both.
	e.Creds.AddMember("stray.member@example.com")
	if err := e.Creds.Revoke(ctx, "anna.kept@example.com"); err != nil {
		t.Fatal(err)
	}
	summary, err := e.Svc.RefreshSubscriptions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := e.Creds.Members(ctx); !slices.Equal(m, []string{"anna.kept@example.com"}) {
		t.Fatalf("members after reconciliation = %v (%s)", m, summary)
	}
	if !strings.Contains(summary, "iam_revoked=1 iam_granted=1") {
		t.Fatalf("summary = %s", summary)
	}

	// A user licensed in Google outside the portal (GOOGLE_ONLY deviation)
	// keeps the roles: Google is the source of truth, an admin decides.
	if err := e.Fake.Seed("console.user@example.com", time.Now(), nil); err != nil {
		t.Fatal(err)
	}
	e.Creds.AddMember("console.user@example.com")
	if _, err := e.Svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	if m, _ := e.Creds.Members(ctx); !slices.Contains(m, "console.user@example.com") {
		t.Fatalf("GOOGLE_ONLY member revoked: %v", m)
	}
}

// Consumption by business unit and GPS code (display only) matches the
// inventory filtered on the same key; the inventory is served by pages.
func TestConsumption(t *testing.T) {
	e := testenv.New(t, 10)
	for i := range 6 {
		_, res := e.Request(t, fmt.Sprintf("dev%d.user@example.com", i))
		mustOutcome(t, res, service.OutcomeAssigned)
	}
	dir, _ := e.Request(t, "anna.dupont.ext@example.com")
	if _, err := e.Svc.Escalate(ctx, e.User("anna.dupont.ext@example.com"), dir.RequestID,
		service.EscalationInput{Division: "D", Entity: "E", Project: "P"}); err != nil {
		t.Fatal(err)
	}
	res, err := e.Svc.Approve(ctx, "alex.martin@example.com", dir.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	mustOutcome(t, res, service.OutcomeAssigned)

	for _, by := range []string{"bu", "gps"} {
		v, err := e.Svc.Consumption(ctx, by)
		if err != nil || v.Total != 7 || len(v.Rows) == 0 {
			t.Fatalf("%s: %+v %v", by, v, err)
		}
		external := 0
		for _, r := range v.Rows {
			if r.Key == nil {
				t.Fatalf("%s: license without directory data", by)
			}
			f := store.InventoryFilter{BusinessUnits: []string{*r.Key}}
			if by == "gps" {
				f = store.InventoryFilter{GPSCodes: []string{strings.ToUpper(*r.Key)}}
				if !r.Allowlisted {
					t.Errorf("gps %s: simulator codes are in the allowlist", *r.Key)
				}
			}
			inv, err := e.Svc.Inventory(ctx, f, 0)
			if err != nil || inv.Total != r.Licenses || r.Unused != 0 {
				t.Errorf("%s %s: licenses=%d unused=%d, inventory=%d %v", by, *r.Key, r.Licenses, r.Unused, inv.Total, err)
			}
			external += r.External
		}
		if external != 1 {
			t.Errorf("%s: external=%d", by, external)
		}
	}
	if _, err := e.Svc.Consumption(ctx, "site"); err == nil {
		t.Error("unknown grouping accepted")
	}

	// Without usage for unused_days, every license counts as unused.
	e.Clock.Advance(8 * 24 * time.Hour)
	if v, _ := e.Svc.Consumption(ctx, "bu"); v.Rows[0].Unused != v.Rows[0].Licenses {
		t.Errorf("unused after 8 days: %+v", v.Rows[0])
	}

	// Pages: most recent first, no overlap, nothing lost.
	seen := map[string]bool{}
	for offset := 0; offset < 7; offset += 3 {
		rows, total, err := e.DB.Inventory(ctx, store.InventoryFilter{}, ".ext", e.Clock.Now(), 3, offset)
		if err != nil || total != 7 {
			t.Fatalf("offset %d: total=%d %v", offset, total, err)
		}
		for _, r := range rows {
			if seen[r.UserEmail] {
				t.Fatalf("%s on two pages", r.UserEmail)
			}
			seen[r.UserEmail] = true
		}
	}
	if len(seen) != 7 {
		t.Fatalf("pages cover %d licenses, want 7", len(seen))
	}
	if inv, _ := e.Svc.Inventory(ctx, store.InventoryFilter{}, -5); inv.Offset != 0 || inv.Limit != service.InventoryPageSize {
		t.Fatalf("offset=%d limit=%d", inv.Offset, inv.Limit)
	}
}

// Back-port of licenses found in Google only: directory check first; a
// technical failure stops it, an unknown / non-eligible user is back-ported
// and flagged; Google's dates are kept (7-day usage rule), no e-mail, IAM
// group joined; the user's panel shows the form steps as not done.
func TestBackport(t *testing.T) {
	e := testenv.New(t, 10)
	now := e.Clock.Now()
	old, used := now.AddDate(0, -2, 0), now.AddDate(0, 0, -3)
	for _, u := range []string{"kim.legacy@example.com", "ann.legacy.ext@example.com", "bob.ghost@example.com",
		"eva.edslow@example.com", "tom.badgps@example.com"} {
		if err := e.Fake.Seed(u, old, &used); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.Svc.RefreshSubscriptions(ctx); err != nil {
		t.Fatal(err)
	}
	devs, _ := e.DB.OpenDeviations(ctx)
	ids := map[string]int64{}
	for _, d := range devs {
		ids[d.UserEmail] = d.ID
	}
	if len(ids) != 5 {
		t.Fatalf("deviations = %v", ids)
	}

	// One by one: eligible user.
	if err := e.Svc.ResolveDeviation(ctx, "alex.martin@example.com", ids["kim.legacy@example.com"], service.DevActionBackport); err != nil {
		t.Fatal(err)
	}
	a, err := e.DB.ActiveAssignment(ctx, "kim.legacy@example.com")
	if err != nil || a.Source != domain.SourceReconciled || a.BackportedAt == nil || a.IneligibleReason != nil ||
		a.BusinessUnit == nil || a.GPSCode == nil || a.DirCheckResult == nil || *a.DirCheckResult != service.CheckOK ||
		a.AssignedAt.Sub(old).Abs() > time.Millisecond || a.LastUsageAt == nil || a.LastUsageAt.Sub(used).Abs() > time.Millisecond {
		t.Fatalf("back-ported record: %+v %v", a, err)
	}
	if members, _ := e.Creds.Members(ctx); !slices.Contains(members, "kim.legacy@example.com") {
		t.Errorf("not in the IAM bundle group: %v", members)
	}
	if n := len(e.Mails.To("kim.legacy@example.com")); n != 0 {
		t.Errorf("%d e-mail(s) sent for a back-port", n)
	}

	// Directory unavailable: nothing back-ported, the deviation stays open.
	err = e.Svc.ResolveDeviation(ctx, "alex.martin@example.com", ids["eva.edslow@example.com"], service.DevActionBackport)
	var ue *service.UserError
	if !errors.As(err, &ue) || ue.Status != http.StatusServiceUnavailable {
		t.Fatalf("directory down: %v", err)
	}
	if _, err := e.DB.ActiveAssignment(ctx, "eva.edslow@example.com"); err == nil {
		t.Fatal("back-ported without a directory check")
	}

	// All the others at once: external, unknown, GPS code outside the allowlist
	// are back-ported and flagged; the directory failure stays pending.
	res, err := e.Svc.BackportAll(ctx, "alex.martin@example.com")
	if err != nil || res.Backported != 0 || res.Flagged != 3 || res.Pending != 1 || res.Remaining != 1 {
		t.Fatalf("backport all: %+v %v", res, err)
	}
	for _, u := range []string{"ann.legacy.ext@example.com", "bob.ghost@example.com", "tom.badgps@example.com"} {
		if a, err := e.DB.ActiveAssignment(ctx, u); err != nil || a.IneligibleReason == nil || a.BackportedAt == nil {
			t.Errorf("%s: %+v %v", u, a, err)
		}
	}
	inv, _ := e.Svc.Inventory(ctx, store.InventoryFilter{Sources: []string{string(domain.SourceReconciled)}}, 0)
	if inv.Total != 4 {
		t.Errorf("back-ported filter: %d", inv.Total)
	}
	if inv, _ = e.Svc.Inventory(ctx, store.InventoryFilter{Sources: []string{string(domain.SourceReconciled)}, DirectoryCheck: []string{"INELIGIBLE"}}, 0); inv.Total != 3 {
		t.Errorf("back-ported and not eligible: %d", inv.Total)
	}
	if pts, err := e.DB.Depths(ctx, e.Cfg.TimeZone.String(), 2); err != nil || pts[len(pts)-1].Backported != 4 || pts[len(pts)-1].GoogleOnly != 1 {
		t.Errorf("depths: %+v %v", pts, err)
	}

	// The user's panel: form steps not done, directory checked, admin validation.
	v, err := e.Svc.Home(ctx, e.User("kim.legacy@example.com"), 1)
	if err != nil || v.State != service.StateActive {
		t.Fatalf("home: %+v %v", v, err)
	}
	want := map[domain.StepID]domain.StepState{domain.StepIdentity: domain.StepKO, domain.StepTraining: domain.StepKO,
		domain.StepDuration: domain.StepKO, domain.StepDetails: domain.StepKO, domain.StepDirectory: domain.StepOK,
		domain.StepAdmin: domain.StepOK, domain.StepCredentials: domain.StepOK, domain.StepLicense: domain.StepOK, domain.StepEmail: domain.StepSkipped}
	for _, st := range v.Steps {
		if st.State != want[st.ID] {
			t.Errorf("step %s = %s, want %s", st.ID, st.State, want[st.ID])
		}
	}
	if v, _ = e.Svc.Home(ctx, e.User("tom.badgps@example.com"), 1); v.Steps[4].State != domain.StepKO || v.Steps[4].Hint == "" {
		t.Errorf("directory step of a flagged user: %+v", v.Steps[4])
	}
}
