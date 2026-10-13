// Package gcp wraps the Google Cloud APIs used by the portal:
//
//   - Cloud Commerce Consumer Procurement API (cloudcommerceconsumerprocurement.googleapis.com)
//     -> orders (= subscriptions) and their Gemini Code Assist license pool:
//     getLicensePool, licensePool:assign, licensePool:unassign,
//     licensePool:enumerateLicensedUsers.
//   - Cloud Identity Groups API (cloudidentity.googleapis.com)
//     -> membership of the group that carries the IAM bundle required to use
//     Gemini Code Assist (see credentials.go).
//
// Each integration has a real implementation and a fake one (local dev,
// demo, tests) behind the same interface.
package gcp

import (
	"context"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Order is a Google Cloud order that may hold a license pool.
type Order struct {
	Name        string // billingAccounts/{ba}/orders/{order}
	DisplayName string
	Offer       string
	Start       *time.Time
	End         *time.Time
	AutoRenew   bool
	Cancelled   bool // every line item is cancelled
	CreateTime  time.Time
}

// ActiveAt reports whether the order's subscription covers t.
func (o Order) ActiveAt(t time.Time) bool {
	if o.Cancelled {
		return false
	}
	if o.Start != nil && t.Before(*o.Start) {
		return false
	}
	if o.End != nil && !t.Before(*o.End) {
		return false
	}
	return true
}

// Pool is a license pool attached to an order.
type Pool struct {
	Name      string // billingAccounts/{ba}/orders/{order}/licensePool
	Total     int
	Available int
	Manual    bool // manual assignment protocol (required to assign / unassign individually)
}

// Assigned returns the number of assigned seats.
func (p Pool) Assigned() int { return p.Total - p.Available }

// LicensedUser is a user holding a license in a pool.
type LicensedUser struct {
	Email       string
	AssignTime  time.Time
	RecentUsage *time.Time // "may not be the most recent usage time, updated within 24 hours" (API doc)
}

// Licensing is the subset of the Procurement API used by the portal.
type Licensing interface {
	// ListOrders lists the orders of the billing account.
	ListOrders(ctx context.Context) ([]Order, error)
	// GetPool returns the license pool of an order.
	GetPool(ctx context.Context, order string) (Pool, error)
	// Enumerate lists every licensed user of an order's pool.
	Enumerate(ctx context.Context, order string) ([]LicensedUser, error)
	// Assign assigns licenses to users.
	Assign(ctx context.Context, order string, emails []string) error
	// Unassign removes licenses from users.
	Unassign(ctx context.Context, order string, emails []string) error
}

// PoolName returns the license pool resource name of an order.
func PoolName(order string) string { return order + "/licensePool" }

// --- Instrumentation -----------------------------------------------------------

// instrumentedLicensing records every call in telemetry.
type instrumentedLicensing struct {
	next Licensing
	rec  *telemetry.Recorder
}

// InstrumentLicensing wraps a Licensing implementation with telemetry.
func InstrumentLicensing(next Licensing, rec *telemetry.Recorder) Licensing {
	return &instrumentedLicensing{next: next, rec: rec}
}

func (i *instrumentedLicensing) ListOrders(ctx context.Context) (out []Order, err error) {
	err = i.rec.Observe(ctx, telemetry.TargetOrders, "listOrders", func(ctx context.Context) error {
		out, err = i.next.ListOrders(ctx)
		return err
	})
	return
}

func (i *instrumentedLicensing) GetPool(ctx context.Context, order string) (out Pool, err error) {
	err = i.rec.Observe(ctx, telemetry.TargetLicense, "getPool", func(ctx context.Context) error {
		out, err = i.next.GetPool(ctx, order)
		return err
	})
	return
}

func (i *instrumentedLicensing) Enumerate(ctx context.Context, order string) (out []LicensedUser, err error) {
	err = i.rec.Observe(ctx, telemetry.TargetLicense, "enumerate", func(ctx context.Context) error {
		out, err = i.next.Enumerate(ctx, order)
		return err
	})
	return
}

func (i *instrumentedLicensing) Assign(ctx context.Context, order string, emails []string) error {
	return i.rec.Observe(ctx, telemetry.TargetLicense, "assign", func(ctx context.Context) error {
		return i.next.Assign(ctx, order, emails)
	})
}

func (i *instrumentedLicensing) Unassign(ctx context.Context, order string, emails []string) error {
	return i.rec.Observe(ctx, telemetry.TargetLicense, "unassign", func(ctx context.Context) error {
		return i.next.Unassign(ctx, order, emails)
	})
}
