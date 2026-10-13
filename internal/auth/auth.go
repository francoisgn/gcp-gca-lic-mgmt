// Package auth resolves the identity of the caller.
//
// In production the portal sits behind Identity-Aware Proxy (IAP): Google
// SSO is enforced by IAP and every request carries a signed JWT in the
// X-Goog-IAP-JWT-Assertion header. We verify its signature and audience and
// trust the e-mail claim — never the plain X-Goog-Authenticated-User-Email
// header, which could be forged if IAP were ever bypassed.
//
// In development (AUTH_MODE=dev) the identity comes from a cookie set by the
// identity switcher, so any role can be tested locally.
package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/api/idtoken"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/roles"
)

// DevCookie is the cookie holding the impersonated e-mail in dev mode.
const DevCookie = "gcalm_dev_user"

// ErrUnauthenticated is returned when no valid identity is found.
var ErrUnauthenticated = errors.New("unauthenticated")

// Identity is the authenticated caller.
type Identity struct {
	Email string
	Role  roles.Role
}

// DisplayName derives "Jane Doe" from "jane.doe@example.com".
func (i Identity) DisplayName() string {
	local, _, _ := strings.Cut(i.Email, "@")
	parts := strings.FieldsFunc(local, func(r rune) bool { return r == '.' || r == '_' || r == '-' })
	for k, p := range parts {
		if p != "" {
			parts[k] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	if len(parts) == 0 {
		return i.Email
	}
	return strings.Join(parts, " ")
}

// Initials returns up to two initials for the avatar.
func (i Identity) Initials() string {
	var out []rune
	for _, w := range strings.Fields(i.DisplayName()) {
		out = append(out, []rune(w)[0])
		if len(out) == 2 {
			break
		}
	}
	return strings.ToUpper(string(out))
}

// Authenticator extracts the e-mail of the caller from a request.
type Authenticator interface {
	Authenticate(r *http.Request) (string, error)
}

// --- IAP -------------------------------------------------------------------

// IAP validates the IAP signed header.
type IAP struct {
	audience  string
	validator *idtoken.Validator
}

// NewIAP builds an IAP authenticator. audience is the value expected in the
// "aud" claim (for Cloud Run with IAP: /projects/NUMBER/locations/REGION/services/NAME,
// for a load balancer backend: /projects/NUMBER/global/backendServices/ID).
func NewIAP(ctx context.Context, audience string) (*IAP, error) {
	v, err := idtoken.NewValidator(ctx)
	if err != nil {
		return nil, err
	}
	return &IAP{audience: audience, validator: v}, nil
}

// Authenticate implements Authenticator.
func (a *IAP) Authenticate(r *http.Request) (string, error) {
	token := r.Header.Get("X-Goog-IAP-JWT-Assertion")
	if token == "" {
		return "", ErrUnauthenticated
	}
	payload, err := a.validator.Validate(r.Context(), token, a.audience)
	if err != nil {
		return "", errors.Join(ErrUnauthenticated, err)
	}
	email, _ := payload.Claims["email"].(string)
	if email == "" {
		return "", ErrUnauthenticated
	}
	return domain.NormalizeEmail(email), nil
}

// --- Dev -------------------------------------------------------------------

// Dev reads the identity from a cookie, falling back to a default user.
type Dev struct{ DefaultUser string }

// Authenticate implements Authenticator.
func (d Dev) Authenticate(r *http.Request) (string, error) {
	if c, err := r.Cookie(DevCookie); err == nil && strings.Contains(c.Value, "@") {
		return domain.NormalizeEmail(c.Value), nil
	}
	if d.DefaultUser == "" {
		return "", ErrUnauthenticated
	}
	return domain.NormalizeEmail(d.DefaultUser), nil
}

// --- Context helpers ---------------------------------------------------------

type ctxKey struct{}

// WithIdentity stores the identity in the request context.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// FromContext returns the identity stored by the middleware.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(ctxKey{}).(Identity)
	return id, ok
}
