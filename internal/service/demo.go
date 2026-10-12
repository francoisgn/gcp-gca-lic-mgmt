package service

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"math/rand/v2"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// =============================================================================
// Simulation panel (GOOGLE_MODE=fake only)
// =============================================================================

// SimRollover simulates a subscription renewal: the current fake order ends
// now and a new one with `seats` seats starts. The refresh job migrates.
func (s *Service) SimRollover(ctx context.Context, admin string, seats int) (string, error) {
	if s.Fake == nil {
		return "", forbidden("Simulation is only available with GOOGLE_MODE=fake.")
	}
	name, err := s.Fake.Rollover(seats)
	if err != nil {
		return "", err
	}
	_ = s.DB.Audit(ctx, admin, "sim.rollover", name, map[string]any{"seats": seats})
	return s.RefreshSubscriptions(ctx)
}

// SimSetSeats changes the size of the fake pool.
func (s *Service) SimSetSeats(ctx context.Context, admin string, seats int) (string, error) {
	if s.Fake == nil {
		return "", forbidden("Simulation is only available with GOOGLE_MODE=fake.")
	}
	if seats < 0 || seats > 10000 {
		return "", badRequest("Invalid number of seats.")
	}
	if err := s.Fake.SetSeats(seats); err != nil {
		return "", err
	}
	_ = s.DB.Audit(ctx, admin, "sim.seats", "", map[string]any{"seats": seats})
	return s.RefreshSubscriptions(ctx)
}

// SimRemoveInGoogle removes a license behind the portal's back (DB_ONLY demo).
func (s *Service) SimRemoveInGoogle(ctx context.Context, admin, email string) error {
	if s.Fake == nil {
		return forbidden("Simulation is only available with GOOGLE_MODE=fake.")
	}
	s.pool.Invalidate()
	_ = s.DB.Audit(ctx, admin, "sim.remove_in_google", email, nil)
	return s.Fake.Remove(domain.NormalizeEmail(email))
}

// =============================================================================
// Demo seed (DEMO_SEED=true, dev only, empty database)
// =============================================================================

type demoActive struct {
	email     string
	daysAgo   float64
	source    domain.AssignmentSource
	duration  domain.Duration
	usageDays float64 // -1 = never used
}

// SeedDemo fills an empty database with 30 days of realistic activity so
// every screen and chart has something to show.
func (s *Service) SeedDemo(ctx context.Context) error {
	if s.Fake == nil {
		return fmt.Errorf("demo seed requires GOOGLE_MODE=fake")
	}
	var n int
	if err := s.DB.Pool.QueryRow(ctx, `SELECT count(*) FROM license_requests`).Scan(&n); err != nil || n > 0 {
		return err
	}
	slog.Info("seeding demo data")
	if _, err := s.RefreshSubscriptions(ctx); err != nil {
		return err
	}
	cur, err := s.DB.CurrentSubscription(ctx)
	if err != nil {
		return err
	}
	if err := s.Fake.SetSeats(12); err != nil {
		return err
	}
	// GPS codes authorized for automatic approval (managed in Admin → GPS codes).
	teams := []string{"Platform engineering", "Data & AI", "Embedded software", "Cloud operations", "Quality & test",
		"Digital factory", "Cybersecurity", "Product lines A", "Product lines B", "R&D tooling", "Mobile apps", "Customer portal"}
	for i, code := range directory.SimulatorGPSCodes {
		if _, err := s.DB.AddGPSCodes(ctx, []string{code}, teams[i%len(teams)], "super.admin@example.com"); err != nil {
			return err
		}
	}
	// Assigned directly in the Google console: shows up as a GOOGLE_ONLY deviation.
	if err := s.Fake.Seed("console.only@example.com", s.Now().AddDate(0, 0, -3), nil); err != nil {
		return err
	}
	now := s.Now()
	rng := rand.New(rand.NewPCG(42, 2026))
	sim := &directory.Simulator{}
	ago := func(days float64) time.Time { return now.Add(-time.Duration(days * 24 * float64(time.Hour))) }
	exec := func(sql string, args ...any) error {
		_, err := s.DB.Pool.Exec(ctx, sql, args...)
		return err
	}

	// newRequest inserts a request with past timestamps and directory data.
	newRequest := func(email string, created time.Time, status domain.RequestStatus, dur domain.Duration) (*store.Request, error) {
		acc := sim.Profile(email)
		r := &store.Request{UserEmail: email, Status: status, TrainingAck: true, Duration: dur, Answers: map[string]string{
			"use_case": "Application development", "ide": "Visual Studio Code", "project": "Demo project",
			"justification": "Speed up code reviews and unit test writing on our services.",
		}}
		if err := s.DB.CreateRequest(ctx, r); err != nil {
			return nil, err
		}
		outcome, reason, err := s.evaluateDirectory(ctx, email, acc, nil)
		if err != nil {
			return nil, err
		}
		s.applyDirectory(r, acc, outcome, reason, created.Add(time.Minute))
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return nil, err
		}
		return r, exec(`UPDATE license_requests SET created_at=$2, updated_at=$2 WHERE id=$1`, r.ID, created)
	}

	// --- Active licenses (12 seats: 11 here + console.only in Google) -------------
	actives := []demoActive{
		{"olivia.brown@example.com", 25, domain.SourceAuto, "3M", 0.2},
		{"ethan.clark@example.com", 21, domain.SourceAuto, "6M", 1},
		{"ava.lewis@example.com", 18, domain.SourceAdmin, "1Y", 0.1},
		{"lucas.walker@example.com", 15, domain.SourceAuto, "1M", 2},
		{"chloe.hall@example.com", 12, domain.SourceAuto, "3M", 5},
		{"hugo.allen@example.com", 9, domain.SourceAuto, "1M", 6.5},
		{"lea.idle@example.com", 6, domain.SourceAdmin, "3M", -1},
		{"adam.king@example.com", 4, domain.SourceAuto, "7d", 1},
		{"zoe.wright@example.com", 2, domain.SourceAuto, "3M", 0.3},
		{"jules.scott@example.com", 1, domain.SourceAuto, "1M", -1},
		{"sacha.green.ext@example.com", 0.2, domain.SourceAdmin, "6M", -1}, // external: admin approval only
	}
	for _, d := range actives {
		at := ago(d.daysAgo)
		r, err := newRequest(d.email, at.Add(-20*time.Minute), domain.ReqAssigned, d.duration)
		if err != nil {
			return err
		}
		var usage *time.Time
		if d.usageDays >= 0 {
			usage = ptr(ago(d.usageDays))
		}
		a := &store.Assignment{UserEmail: d.email, SubscriptionID: cur.ID, RequestID: &r.ID, Source: d.source,
			Duration: d.duration, AssignedAt: at, ExpiresAt: d.duration.AddTo(at), LastUsageAt: usage,
			Site: r.DirSite, BusinessUnit: r.DirBusinessUnit, GPSCode: r.DirGPSCode}
		if d.source == domain.SourceAdmin {
			a.AssignedBy = ptr("alex.martin@example.com")
			r.EscalatedAt, r.EscDivision, r.EscEntity, r.EscProject = ptr(at.Add(-2*time.Hour)), ptr("Engineering"), ptr("Platform"), ptr("Developer portal")
			r.Decision, r.DecidedAt, r.DecidedBy = ptr(domain.DecisionApproved), ptr(at.Add(-time.Minute)), ptr("alex.martin@example.com")
		}
		if err := s.DB.InsertAssignment(ctx, a); err != nil {
			return err
		}
		r.CredentialsAt, r.LicenseAt, r.EmailAt, r.CompletedAt, r.AssignmentID = &at, &at, &at, &at, &a.ID
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
		if err := s.Fake.Seed(d.email, at, usage); err != nil {
			return err
		}
	}

	// --- License history (ended assignments, waterfall) ------------------------------
	reasons := []domain.EndReason{domain.EndUnused, domain.EndUnused, domain.EndExpired, domain.EndAdminRemoval, domain.EndExpired}
	for i := 0; i < 26; i++ {
		email := fmt.Sprintf("former.user%02d@example.com", i+1)
		if i == 0 {
			email = "jane.doe@example.com" // the default dev user has a previous license
		}
		endDays := 1 + rng.Float64()*28
		startDays := endDays + 7 + rng.Float64()*30
		at, end := ago(startDays), ago(endDays)
		src := domain.SourceAuto
		if rng.IntN(4) == 0 {
			src = domain.SourceAdmin
		}
		r, err := newRequest(email, at.Add(-time.Hour), domain.ReqAssigned, "1M")
		if err != nil {
			return err
		}
		a := &store.Assignment{UserEmail: email, SubscriptionID: cur.ID, RequestID: &r.ID, Source: src, Duration: "1M",
			AssignedAt: at, ExpiresAt: domain.Duration("1M").AddTo(at), Site: r.DirSite, BusinessUnit: r.DirBusinessUnit, GPSCode: r.DirGPSCode}
		if err := s.DB.InsertAssignment(ctx, a); err != nil {
			return err
		}
		reason := reasons[i%len(reasons)]
		actor := "system"
		if reason == domain.EndAdminRemoval {
			actor = "sam.lee@example.com"
		}
		if err := s.DB.EndAssignment(ctx, a.ID, reason, actor, end); err != nil {
			return err
		}
		r.LicenseAt, r.CompletedAt, r.EmailAt, r.CredentialsAt, r.AssignmentID = &at, &at, &at, &at, &a.ID
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
	}

	// --- Waiting list (current + history for the depth chart) -------------------------
	for i, q := range []struct {
		email string
		days  float64
	}{{"noah.wilson@example.com", 3.2}, {"emma.davis@example.com", 2.1}, {"leo.martin@example.com", 0.8}} {
		r, err := newRequest(q.email, ago(q.days), domain.ReqQueued, "3M")
		if err != nil {
			return err
		}
		r.QueuedAt = ptr(ago(q.days))
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
		if _, err := s.DB.Enqueue(ctx, r.ID, q.email, domain.PriorityNormal, ago(q.days)); err != nil {
			return err
		}
		_ = i
	}
	for i := 0; i < 14; i++ {
		email := fmt.Sprintf("queued.past%02d@example.com", i+1)
		in := 2 + rng.Float64()*26
		out := in - (0.3 + rng.Float64()*4)
		if out < 0.1 {
			out = 0.1
		}
		r, err := newRequest(email, ago(in), domain.ReqAbandoned, "1M")
		if err != nil {
			return err
		}
		if err := exec(`INSERT INTO queue_entries (request_id, user_email, priority, enqueued_at, status, left_at)
			VALUES ($1,$2,0,$3,$4,$5)`, r.ID, email, ago(in), []string{"SERVED", "SERVED", "LEFT"}[i%3], ago(out)); err != nil {
			return err
		}
	}

	// --- Escalations: pending and rejected ----------------------------------------------
	for _, p := range []struct {
		email string
		hours float64
	}{{"mia.ko@example.com", 26}, {"tom.nogps@example.com", 5}} {
		r, err := newRequest(p.email, ago(p.hours/24), domain.ReqPendingAdmin, "3M")
		if err != nil {
			return err
		}
		r.EscalatedAt, r.EscDivision, r.EscEntity = ptr(ago(p.hours/24)), ptr("Powertrain"), ptr("Software Factory")
		r.EscProject, r.EscComment = ptr("ADAS validation tooling"), ptr("I joined the team last month, my directory profile is not up to date yet.")
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
	}
	// An external account waiting for an administrator.
	{
		r, err := newRequest("lisa.martin.ext@example.com", ago(0.3), domain.ReqPendingAdmin, "1M")
		if err != nil {
			return err
		}
		r.EscalatedAt, r.EscDivision, r.EscEntity = ptr(ago(0.3)), ptr("Engineering"), ptr("Partner team")
		r.EscProject, r.EscComment = ptr("Payments API migration"), ptr("Contractor on a 3-month mission, sponsored by my manager.")
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
	}
	for _, days := range []float64{10, 4} {
		r, err := newRequest("liam.ko@example.com", ago(days), domain.ReqRejected, "6M")
		if err != nil {
			return err
		}
		r.EscalatedAt, r.EscDivision, r.EscEntity, r.EscProject = ptr(ago(days)), ptr("Comfort"), ptr("Lighting"), ptr("Firmware update")
		r.EscComment = ptr("Contractor, need it for a 6 month mission.")
		r.Decision, r.DecidedAt, r.DecidedBy = ptr(domain.DecisionRejected), ptr(ago(days-0.5)), ptr("sam.lee@example.com")
		r.DecisionReason = ptr("Contractors must request through their own company subscription.")
		r.CompletedAt = r.DecidedAt
		if err := s.DB.UpdateRequest(ctx, r); err != nil {
			return err
		}
	}

	// --- Buffer: one pending (Google timeout on the license) + history --------------------
	r, err := newRequest("sofia.slow@example.com", ago(0.02), domain.ReqBuffered, "1M")
	if err != nil {
		return err
	}
	dirOK := domain.DirOK
	r.DirOutcome, r.BufferedAt, r.CredentialsAt = &dirOK, ptr(ago(0.02)), ptr(ago(0.02))
	if err := s.DB.UpdateRequest(ctx, r); err != nil {
		return err
	}
	if _, err := s.DB.UpsertBuffer(ctx, r.ID, r.UserEmail, domain.StageLicense, "context deadline exceeded", false, ago(0.02)); err != nil {
		return err
	}
	for i := 0; i < 9; i++ {
		in := 1 + rng.Float64()*27
		email := fmt.Sprintf("buffered.past%02d@example.com", i+1)
		pr, err := newRequest(email, ago(in), domain.ReqAssigned, "1M")
		if err != nil {
			return err
		}
		if err := exec(`INSERT INTO buffer_entries (request_id, user_email, stage, created_at, attempts, status, resolved_at)
			VALUES ($1,$2,$3,$4,$5,'DONE',$6)`, pr.ID, email, []string{"LICENSE", "CREDENTIALS"}[i%2],
			ago(in), 1+rng.IntN(3), ago(in-0.01-rng.Float64()*0.2)); err != nil {
			return err
		}
	}

	// --- DB_ONLY deviation: a record the portal believes active, absent in Google ----
	gr, err := newRequest("max.orphan@example.com", ago(8), domain.ReqAssigned, "3M")
	if err != nil {
		return err
	}
	if err := s.DB.InsertAssignment(ctx, &store.Assignment{UserEmail: "max.orphan@example.com", SubscriptionID: cur.ID,
		RequestID: &gr.ID, Source: domain.SourceAuto, Duration: "3M", AssignedAt: ago(8), ExpiresAt: domain.Duration("3M").AddTo(ago(8)),
		LastUsageAt: ptr(ago(1)), Site: gr.DirSite, BusinessUnit: gr.DirBusinessUnit, GPSCode: gr.DirGPSCode}); err != nil {
		return err
	}
	if err := exec(`INSERT INTO deviations (user_email, subscription_id, kind, detected_at, last_seen_at, resolved_at, resolution, resolved_by)
		VALUES ('old.console@example.com',$1,'GOOGLE_ONLY',$2,$2,$3,'REMOVED_FROM_GOOGLE','super.admin@example.com')`, cur.ID, ago(12), ago(9)); err != nil {
		return err
	}

	// --- Monthly directory re-check results (inventory flags) ----------------------------
	if err := exec(`UPDATE license_assignments SET dir_checked_at = now() - interval '3 days', dir_check_result = 'OK',
		dir_current_status = 'OK', dir_current_site = site, dir_current_business_unit = business_unit, dir_current_gps_code = gps_code
		WHERE ended_at IS NULL AND assigned_at < now() - interval '10 days'`); err != nil {
		return err
	}
	if err := exec(`UPDATE license_assignments SET dir_check_result = 'CHANGED', dir_current_site = 'MUN',
		drift_fields = '{site}', drift_detected_at = now() - interval '2 days' WHERE user_email = 'chloe.hall@example.com' AND ended_at IS NULL`); err != nil {
		return err
	}
	if err := exec(`UPDATE license_assignments SET dir_check_result = 'INELIGIBLE', dir_current_gps_code = '0badc0de',
		drift_fields = '{gps_code}', drift_detected_at = now() - interval '1 day',
		ineligible_reason = 'Your GPS code (0b••••de) is not in the list of codes authorized for automatic approval.'
		WHERE user_email = 'hugo.allen@example.com' AND ended_at IS NULL`); err != nil {
		return err
	}
	if err := exec(`UPDATE license_assignments SET dir_check_result = 'ERROR', dir_check_error = 'context deadline exceeded'
		WHERE user_email = 'ethan.clark@example.com' AND ended_at IS NULL`); err != nil {
		return err
	}

	// --- Notification journal (history) ------------------------------------------------------
	for _, n := range []struct{ event, to, subject string }{
		{EvLicenseAssigned, "olivia.brown@example.com", "Your Gemini Code Assist license is active"},
		{EvQueued, "noah.wilson@example.com", "You are in the Gemini Code Assist waiting list"},
		{EvRequestEscalated, "mia.ko@example.com", "Your Gemini Code Assist request is under review"},
		{EvAdminEscalation, "alex.martin@example.com", "[GCA portal] New request to review: mia.ko@example.com"},
		{EvRequestRejected, "liam.ko@example.com", "Your Gemini Code Assist request was rejected"},
		{EvRemovedUnused, "jane.doe@example.com", "Your Gemini Code Assist license was removed"},
		{EvBuffered, "sofia.slow@example.com", "Your Gemini Code Assist request is delayed"},
	} {
		if err := s.DB.InsertNotification(ctx, store.Notification{Event: n.event, Recipient: n.to, Subject: n.subject,
			BodyText: "(demo data)", Status: "SENT"}); err != nil {
			return err
		}
	}

	// --- Daily stats and technical KPIs ---------------------------------------------------
	for d := 29; d >= 0; d-- {
		day := ago(float64(d)).In(s.Cfg.TimeZone)
		seats := 12
		if d > 15 {
			seats = 10
		}
		pv := 60 + rng.IntN(180)
		if wd := day.Weekday(); wd == time.Saturday || wd == time.Sunday {
			pv /= 5
		}
		if err := exec(`INSERT INTO daily_stats (day, page_views, seats_total, seats_assigned_google) VALUES ($1::date,$2,$3,$4)
			ON CONFLICT (day) DO UPDATE SET page_views=EXCLUDED.page_views, seats_total=EXCLUDED.seats_total`,
			day.Format("2006-01-02"), pv, seats, seats-rng.IntN(2)); err != nil {
			return err
		}
	}
	type callSpec struct {
		target, op, kind string
		perDay           int
		medianMS         float64
		outcomes         map[string]float64
		timeoutMS        int
	}
	specs := []callSpec{
		{telemetry.TargetDirectory, "lookup", "UX", 30, 220, map[string]float64{"TIMEOUT": .04, "HTTP_5XX": .05, "NETWORK": .015, "NOT_FOUND": .03, "RATE_LIMITED": .01}, 5000},
		{telemetry.TargetDirectory, "lookup", "JOB", 6, 260, map[string]float64{"TIMEOUT": .12, "HTTP_5XX": .06}, 5000},
		{telemetry.TargetDirectory, "lookup", "ADMIN", 1, 240, map[string]float64{"TIMEOUT": .05}, 5000},
		{telemetry.TargetCredentials, "grant", "UX", 8, 420, map[string]float64{"TIMEOUT": .03}, 10000},
		{telemetry.TargetCredentials, "grant", "JOB", 3, 380, map[string]float64{"TIMEOUT": .05}, 30000},
		{telemetry.TargetLicense, "assign", "UX", 8, 1300, map[string]float64{"TIMEOUT": .06, "HTTP_5XX": .02}, 10000},
		{telemetry.TargetLicense, "assign", "JOB", 4, 1100, map[string]float64{"TIMEOUT": .04}, 30000},
		{telemetry.TargetLicense, "enumerate", "UX", 70, 650, map[string]float64{"HTTP_5XX": .01}, 15000},
		{telemetry.TargetLicense, "getPool", "UX", 70, 240, map[string]float64{}, 15000},
		{telemetry.TargetOrders, "listOrders", "JOB", 8, 480, map[string]float64{}, 30000},
	}
	batch := 0
	for d := 9; d >= 0; d-- {
		for _, sp := range specs {
			n := sp.perDay/2 + rng.IntN(sp.perDay+1)
			for k := 0; k < n; k++ {
				ts := ago(float64(d) + rng.Float64())
				if ts.After(now) {
					continue
				}
				outcome := "OK"
				x := rng.Float64()
				for o, p := range sp.outcomes {
					if x < p {
						outcome = o
						break
					}
					x -= p
				}
				dur := int(sp.medianMS * math.Exp(rng.NormFloat64()*0.45))
				if outcome == "TIMEOUT" {
					dur = sp.timeoutMS
				}
				if err := exec(`INSERT INTO api_calls (ts, target, operation, context, duration_ms, outcome) VALUES ($1,$2,$3,$4,$5,$6)`,
					ts, sp.target, sp.op, sp.kind, dur, outcome); err != nil {
					return err
				}
				batch++
			}
		}
	}
	if err := exec(`INSERT INTO feedback (created_at, user_email, category, message, tracker_key, tracker_url) VALUES
		($1, 'olivia.brown@example.com', 'IMPROVEMENT', 'It would be great to be notified one week before my license expires.', 'GCA-42', 'https://jira.example.com/browse/GCA-42')`, ago(3)); err != nil {
		return err
	}
	slog.Info("demo data seeded", "api_calls", batch)
	_, err = s.RefreshSubscriptions(ctx)
	return err
}
