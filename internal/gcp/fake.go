package gcp

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// FakeBillingAccount is the billing account used by the fake implementation.
const FakeBillingAccount = "billingAccounts/FAKE00-000000-000000"

// FakeLicensing simulates the Procurement API for local development, demos
// and tests. State is kept in memory and persisted to a JSON file so it
// survives restarts (and so deviations with the database stay meaningful).
//
// Simulation rules:
//   - every call waits a random latency (0..maxLatency) and honours ctx;
//   - with probability hangRate a call hangs until ctx expires (timeout);
//   - the FIRST assign of an e-mail with the "slow" marker hangs (buffer demo);
//   - users with the "idle" marker never use their license (purge demo).
//
// Markers are tokens of the local part: "jane.slow@example.com".
type FakeLicensing struct {
	mu         sync.Mutex
	path       string
	maxLatency time.Duration
	hangRate   float64
	attempts   map[string]int // assign attempts per e-mail (for the "slow" rule)
	st         fakeState
}

type fakeState struct {
	Orders []*fakeOrder `json:"orders"`
}

type fakeOrder struct {
	ID          string               `json:"id"`
	DisplayName string               `json:"displayName"`
	Offer       string               `json:"offer"`
	Start       time.Time            `json:"start"`
	End         time.Time            `json:"end"`
	Seats       int                  `json:"seats"`
	Cancelled   bool                 `json:"cancelled"`
	Users       map[string]*fakeUser `json:"users"`
}

type fakeUser struct {
	AssignTime time.Time  `json:"assignTime"`
	LastUsage  *time.Time `json:"lastUsage,omitempty"` // explicit usage (seeded), otherwise simulated
}

// NewFakeLicensing loads (or seeds) the fake state.
func NewFakeLicensing(path string, seats int, maxLatency time.Duration, hangRate float64) (*FakeLicensing, error) {
	f := &FakeLicensing{path: path, maxLatency: maxLatency, hangRate: hangRate, attempts: map[string]int{}}
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &f.st); err != nil {
			return nil, fmt.Errorf("fake google state %s: %w", path, err)
		}
	}
	if len(f.st.Orders) == 0 {
		now := time.Now().UTC().Truncate(time.Hour)
		f.st.Orders = []*fakeOrder{{
			ID:          "gca-" + now.AddDate(0, -6, 0).Format("2006-01"),
			DisplayName: "Gemini Code Assist Enterprise — annual",
			Offer:       "services/cloudaicompanion.googleapis.com/standardOffers/gemini-code-assist-enterprise-annual",
			Start:       now.AddDate(0, -6, 0),
			End:         now.AddDate(0, 6, 0),
			Seats:       seats,
			Users:       map[string]*fakeUser{},
		}}
		if err := f.saveLocked(); err != nil {
			return nil, err
		}
	}
	return f, nil
}

func (f *FakeLicensing) orderName(id string) string { return FakeBillingAccount + "/orders/" + id }

// find returns the order referenced by an order resource name.
func (f *FakeLicensing) find(order string) (*fakeOrder, error) {
	id := order[strings.LastIndex(order, "/")+1:]
	for _, o := range f.st.Orders {
		if o.ID == id {
			return o, nil
		}
	}
	return nil, fakeHTTPError{code: 404, msg: "order " + order + " not found"}
}

// simulate waits a random latency or hangs until ctx expires.
func (f *FakeLicensing) simulate(ctx context.Context, hang bool) error {
	if hang || (f.hangRate > 0 && rand.Float64() < f.hangRate) {
		<-ctx.Done()
		return ctx.Err()
	}
	if f.maxLatency <= 0 {
		return ctx.Err()
	}
	select {
	case <-time.After(time.Duration(rand.Int64N(int64(f.maxLatency)))):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ListOrders implements Licensing.
func (f *FakeLicensing) ListOrders(ctx context.Context) ([]Order, error) {
	if err := f.simulate(ctx, false); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Order
	for _, o := range f.st.Orders {
		start, end := o.Start, o.End
		out = append(out, Order{
			Name: f.orderName(o.ID), DisplayName: o.DisplayName, Offer: o.Offer,
			Start: &start, End: &end, AutoRenew: false, Cancelled: o.Cancelled, CreateTime: o.Start,
		})
	}
	return out, nil
}

// GetPool implements Licensing.
func (f *FakeLicensing) GetPool(ctx context.Context, order string) (Pool, error) {
	if err := f.simulate(ctx, false); err != nil {
		return Pool{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, err := f.find(order)
	if err != nil {
		return Pool{}, err
	}
	seats := o.Seats
	if !o.activeAt(time.Now()) {
		seats = 0
	}
	avail := seats - len(o.Users)
	if avail < 0 {
		avail = 0
	}
	return Pool{Name: PoolName(order), Total: seats, Available: avail, Manual: true}, nil
}

// Enumerate implements Licensing.
func (f *FakeLicensing) Enumerate(ctx context.Context, order string) ([]LicensedUser, error) {
	if err := f.simulate(ctx, false); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, err := f.find(order)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]LicensedUser, 0, len(o.Users))
	for email, u := range o.Users {
		out = append(out, LicensedUser{Email: email, AssignTime: u.AssignTime, RecentUsage: simulatedUsage(email, u, now)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Email < out[j].Email })
	return out, nil
}

// simulatedUsage returns a deterministic "recent usage" for a fake user.
func simulatedUsage(email string, u *fakeUser, now time.Time) *time.Time {
	if u.LastUsage != nil {
		return u.LastUsage
	}
	if hasMarker(email, "idle") || now.Sub(u.AssignTime) < time.Hour {
		return nil
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(email))
	t := now.Add(-time.Duration(h.Sum32()%48) * time.Hour)
	if t.Before(u.AssignTime) {
		t = u.AssignTime.Add(time.Hour)
	}
	return &t
}

// Assign implements Licensing.
func (f *FakeLicensing) Assign(ctx context.Context, order string, emails []string) error {
	f.mu.Lock()
	hang := false
	for _, e := range emails {
		f.attempts[e]++
		if hasMarker(e, "slow") && f.attempts[e] == 1 {
			hang = true
		}
	}
	f.mu.Unlock()
	if err := f.simulate(ctx, hang); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, err := f.find(order)
	if err != nil {
		return err
	}
	if !o.activeAt(time.Now()) {
		return fakeHTTPError{code: 400, msg: "subscription is not active"}
	}
	var toAdd []string
	for _, e := range emails {
		if _, ok := o.Users[e]; !ok {
			toAdd = append(toAdd, e)
		}
	}
	if len(o.Users)+len(toAdd) > o.Seats {
		return fakeHTTPError{code: 400, msg: "FAILED_PRECONDITION: not enough licenses available in the pool"}
	}
	for _, e := range toAdd {
		o.Users[e] = &fakeUser{AssignTime: time.Now().UTC()}
	}
	return f.saveLocked()
}

// Unassign implements Licensing.
func (f *FakeLicensing) Unassign(ctx context.Context, order string, emails []string) error {
	if err := f.simulate(ctx, false); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	o, err := f.find(order)
	if err != nil {
		return err
	}
	for _, e := range emails {
		delete(o.Users, e)
	}
	return f.saveLocked()
}

// --- Simulation helpers (dev panel, demo seed, tests) ---------------------------

// Rollover ends the current subscription now and creates a new one with the
// given number of seats, to exercise the migration logic.
func (f *FakeLicensing) Rollover(seats int) (string, error) {
	return f.newOrder(seats, true)
}

// AddSubscription adds a newer subscription WITHOUT ending the current one:
// Google may keep showing the previous order active, the portal must still
// switch to the newest one.
func (f *FakeLicensing) AddSubscription(seats int) (string, error) {
	return f.newOrder(seats, false)
}

func (f *FakeLicensing) newOrder(seats int, endPrevious bool) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now().UTC().Truncate(time.Second)
	for _, o := range f.st.Orders {
		if endPrevious && o.activeAt(now) {
			o.End = now
		}
	}
	id := fmt.Sprintf("gca-%s-%d", now.Format("2006-01"), len(f.st.Orders)+1)
	f.st.Orders = append(f.st.Orders, &fakeOrder{
		ID: id, DisplayName: "Gemini Code Assist Enterprise — renewal " + now.Format("Jan 2006"),
		Offer: f.st.Orders[0].Offer, Start: now, End: now.AddDate(1, 0, 0), Seats: seats,
		Users: map[string]*fakeUser{},
	})
	return f.orderName(id), f.saveLocked()
}

// SetSeats changes the number of seats of the active subscription.
func (f *FakeLicensing) SetSeats(seats int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.st.Orders {
		if o.activeAt(time.Now()) {
			o.Seats = seats
		}
	}
	return f.saveLocked()
}

// Seed forces a user into the active pool with explicit dates (demo data).
func (f *FakeLicensing) Seed(email string, assignTime time.Time, lastUsage *time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.st.Orders {
		if o.activeAt(time.Now()) {
			o.Users[email] = &fakeUser{AssignTime: assignTime, LastUsage: lastUsage}
			return f.saveLocked()
		}
	}
	return fmt.Errorf("no active fake order")
}

// Remove deletes a user from every pool behind the portal's back (demo of a
// DB_ONLY deviation).
func (f *FakeLicensing) Remove(email string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, o := range f.st.Orders {
		delete(o.Users, email)
	}
	return f.saveLocked()
}

func (o *fakeOrder) activeAt(t time.Time) bool {
	return !o.Cancelled && !t.Before(o.Start) && t.Before(o.End)
}

// saveLocked persists the state (caller holds the lock).
func (f *FakeLicensing) saveLocked() error {
	if f.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(f.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(f.st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(f.path, raw, 0o644); err != nil {
		slog.Warn("cannot persist fake google state", "err", err)
	}
	return nil
}

// fakeHTTPError mimics an HTTP error of the real API.
type fakeHTTPError struct {
	code int
	msg  string
}

func (e fakeHTTPError) Error() string {
	return fmt.Sprintf("fake google api: HTTP %d: %s", e.code, e.msg)
}
func (e fakeHTTPError) StatusCode() int { return e.code }

// hasMarker reports whether the local part of an e-mail contains a marker
// token (tokens are separated by '.', '+', '-' or '_').
func hasMarker(email, marker string) bool {
	local, _, _ := strings.Cut(strings.ToLower(email), "@")
	for _, t := range strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '+' || r == '-' || r == '_' }) {
		if t == marker {
			return true
		}
	}
	return false
}
