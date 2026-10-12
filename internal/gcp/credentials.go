package gcp

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"sync"
	"time"

	cloudidentity "google.golang.org/api/cloudidentity/v1"
	"google.golang.org/api/googleapi"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Credentials grants / revokes the Google Cloud permissions required to use
// Gemini Code Assist Standard / Enterprise.
//
// Design ("IAM bundle"): instead of adding per-user IAM bindings (slow,
// asynchronous, limited to 1,500 principals per policy, risk of concurrent
// policy updates), the two required roles
//
//	roles/cloudaicompanion.user              (Gemini for Google Cloud User)
//	roles/serviceusage.serviceUsageConsumer  (Service Usage Consumer)
//
// are bound ONCE (Terraform) to a Google group on the Gemini project. Granting
// credentials = adding the user to the group, revoking = removing them.
// A single synchronous API call, no IAM policy concurrency.
type Credentials interface {
	Grant(ctx context.Context, email string) error
	Revoke(ctx context.Context, email string) error
}

// --- Cloud Identity group -----------------------------------------------------

// GroupCredentials manages membership of the IAM bundle group with the Cloud
// Identity Groups API. The runtime service account must be a MANAGER (or
// OWNER) of the group, or hold the Groups Admin role.
type GroupCredentials struct {
	svc        *cloudidentity.Service
	groupEmail string

	mu        sync.Mutex
	groupName string // groups/{id}, resolved lazily and cached once found
}

// NewGroupCredentials creates the Cloud Identity client with ADC.
func NewGroupCredentials(ctx context.Context, groupEmail string) (*GroupCredentials, error) {
	svc, err := cloudidentity.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("cloud identity client: %w", err)
	}
	return &GroupCredentials{svc: svc, groupEmail: strings.ToLower(groupEmail)}, nil
}

// group resolves groups/{id} from the group e-mail. Only successful lookups
// are cached, so a transient failure is retried on the next call.
func (g *GroupCredentials) group(ctx context.Context) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.groupName != "" {
		return g.groupName, nil
	}
	resp, err := g.svc.Groups.Lookup().GroupKeyId(g.groupEmail).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("lookup group %s: %w", g.groupEmail, err)
	}
	g.groupName = resp.Name
	return g.groupName, nil
}

// Grant implements Credentials. Already being a member is a success.
func (g *GroupCredentials) Grant(ctx context.Context, email string) error {
	parent, err := g.group(ctx)
	if err != nil {
		return err
	}
	_, err = g.svc.Groups.Memberships.Create(parent, &cloudidentity.Membership{
		PreferredMemberKey: &cloudidentity.EntityKey{Id: email},
		Roles:              []*cloudidentity.MembershipRole{{Name: "MEMBER"}},
	}).Context(ctx).Do()
	var gErr *googleapi.Error
	if errors.As(err, &gErr) && gErr.Code == http.StatusConflict {
		return nil
	}
	return err
}

// Revoke implements Credentials. Not being a member is a success.
func (g *GroupCredentials) Revoke(ctx context.Context, email string) error {
	parent, err := g.group(ctx)
	if err != nil {
		return err
	}
	m, err := g.svc.Groups.Memberships.Lookup(parent).MemberKeyId(email).Context(ctx).Do()
	var gErr *googleapi.Error
	if errors.As(err, &gErr) && gErr.Code == http.StatusNotFound {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = g.svc.Groups.Memberships.Delete(m.Name).Context(ctx).Do()
	if errors.As(err, &gErr) && gErr.Code == http.StatusNotFound {
		return nil
	}
	return err
}

// --- No-op (roles granted another way, e.g. to the whole domain) ------------

// NoCredentials is used when the IAM roles are granted outside the portal.
type NoCredentials struct{}

func (NoCredentials) Grant(context.Context, string) error  { return nil }
func (NoCredentials) Revoke(context.Context, string) error { return nil }

// --- Fake --------------------------------------------------------------------

// FakeCredentials simulates the group API: random latency, and the FIRST
// grant of an e-mail with the "iamslow" marker hangs (buffer demo).
type FakeCredentials struct {
	MaxLatency time.Duration
	mu         sync.Mutex
	attempts   map[string]int
}

// Grant implements Credentials.
func (f *FakeCredentials) Grant(ctx context.Context, email string) error {
	f.mu.Lock()
	if f.attempts == nil {
		f.attempts = map[string]int{}
	}
	f.attempts[email]++
	hang := hasMarker(email, "iamslow") && f.attempts[email] == 1
	f.mu.Unlock()
	return fakeWait(ctx, f.MaxLatency, hang)
}

// Revoke implements Credentials.
func (f *FakeCredentials) Revoke(ctx context.Context, _ string) error {
	return fakeWait(ctx, f.MaxLatency, false)
}

func fakeWait(ctx context.Context, max time.Duration, hang bool) error {
	if hang {
		<-ctx.Done()
		return ctx.Err()
	}
	if max <= 0 {
		return nil
	}
	select {
	case <-time.After(time.Duration(rand.Int64N(int64(max)))):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// --- Instrumentation -----------------------------------------------------------

type instrumentedCredentials struct {
	next Credentials
	rec  *telemetry.Recorder
}

// InstrumentCredentials wraps a Credentials implementation with telemetry.
func InstrumentCredentials(next Credentials, rec *telemetry.Recorder) Credentials {
	return &instrumentedCredentials{next: next, rec: rec}
}

func (i *instrumentedCredentials) Grant(ctx context.Context, email string) error {
	return i.rec.Observe(ctx, telemetry.TargetCredentials, "grant", func(ctx context.Context) error {
		return i.next.Grant(ctx, email)
	})
}

func (i *instrumentedCredentials) Revoke(ctx context.Context, email string) error {
	return i.rec.Observe(ctx, telemetry.TargetCredentials, "revoke", func(ctx context.Context) error {
		return i.next.Revoke(ctx, email)
	})
}
