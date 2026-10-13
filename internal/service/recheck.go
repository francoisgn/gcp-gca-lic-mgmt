package service

import (
	"context"
	"fmt"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/directory"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Directory re-check results.
const (
	CheckOK         = "OK"
	CheckChanged    = "CHANGED"
	CheckIneligible = "INELIGIBLE"
	CheckError      = "ERROR"
)

// RecheckDirectory is the "directory-recheck" job (nightly). Every active
// license is checked against the enterprise directory once a month, on the
// anniversary day of its assignment (catch-up after 35 days without a
// successful check). The load is spread evenly: ~1/30 of the licenses per night.
//
// Results (inventory filters "Directory check"):
//   - CHANGED    site, business unit or GPS code differ from the values
//     recorded at assignment (an admin can accept the new values);
//   - INELIGIBLE the user would not be auto-approved today (account KO, not
//     found, external, GPS code missing / not in the allowlist...);
//   - ERROR      the directory did not answer: retried the next night.
//
// Nothing is removed automatically: administrators decide (filter + mass removal).
func (s *Service) RecheckDirectory(ctx context.Context) (string, error) {
	rc := s.Policy.DirectoryRecheck
	if !rc.On() {
		return "disabled by policy", nil
	}
	due, err := s.DB.DueForDirectoryCheck(ctx, s.Cfg.TimeZone.String(), rc.MaxPerRun)
	if err != nil {
		return "", err
	}
	var mu sync.Mutex
	counts := map[string]int{}
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(rc.Concurrency) // gentle with the directory
	for _, a := range due {
		g.Go(func() error {
			res, err := s.recheckOne(gctx, a)
			if err != nil {
				return err // database error: stop the run
			}
			mu.Lock()
			counts[res]++
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return "", err
	}
	return fmt.Sprintf("checked=%d ok=%d changed=%d ineligible=%d errors=%d", len(due),
		counts[CheckOK], counts[CheckChanged], counts[CheckIneligible], counts[CheckError]), nil
}

// RecheckUser re-checks one user's license on demand (admin action).
func (s *Service) RecheckUser(ctx context.Context, admin, email string) (string, error) {
	a, err := s.DB.ActiveAssignment(ctx, email)
	if err != nil {
		return "", notFound("%s has no active license.", email)
	}
	_ = s.DB.Audit(ctx, admin, "directory.recheck", email, nil)
	return s.recheckOne(telemetry.WithKind(ctx, telemetry.KindAdmin), *a)
}

// recheckOne checks one license and records the result.
func (s *Service) recheckOne(ctx context.Context, a store.Assignment) (string, error) {
	lctx, cancel := context.WithTimeout(telemetry.WithKind(ctx, telemetry.KindFromOr(ctx, telemetry.KindJob)), 2*s.Cfg.DirectoryTimeout)
	acc, err := s.Directory.Lookup(lctx, a.UserEmail)
	cancel()
	now := s.Now()
	switch {
	case directory.IsNotFound(err):
		return CheckIneligible, s.DB.SetDirectoryCheck(ctx, a.ID, store.DirectoryCheck{
			Result: CheckIneligible, IneligibleReason: "Account not found in the enterprise directory."}, now)
	case err != nil:
		return CheckError, s.DB.SetDirectoryCheckError(ctx, a.ID, err.Error())
	}

	check := store.DirectoryCheck{Status: acc.Status, Site: acc.Site, BusinessUnit: acc.BusinessUnit, GPSCode: acc.GPSCode}
	// Drift against the values recorded at assignment (no baseline = no drift:
	// the current values become the baseline).
	for _, f := range []struct {
		name     string
		baseline *string
		current  string
	}{{"site", a.Site, acc.Site}, {"business_unit", a.BusinessUnit, acc.BusinessUnit}, {"gps_code", a.GPSCode, acc.GPSCode}} {
		if f.baseline != nil && *f.baseline != f.current {
			check.DriftFields = append(check.DriftFields, f.name)
		}
	}
	ok, reason, err := s.eligible(ctx, a.UserEmail, acc)
	if err != nil {
		return "", err
	}
	switch {
	case !ok:
		check.Result, check.IneligibleReason = CheckIneligible, reason
	case len(check.DriftFields) > 0:
		check.Result = CheckChanged
	default:
		check.Result = CheckOK
	}
	return check.Result, s.DB.SetDirectoryCheck(ctx, a.ID, check, now)
}
