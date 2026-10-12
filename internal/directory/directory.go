// Package directory talks to the enterprise directory (ED): given an e-mail it
// returns the account status, the attachment site, the business unit and the
// GPS code. The GPS code drives the automatic approval (see policy).
//
// API contract (implemented by the real ED adapter and by the simulator):
//
//	GET {base}/v1/accounts?email=jane.doe@example.com
//	Authorization: Bearer {api key}
//	200 {"email":"jane.doe@example.com","status":"OK","site":"PRS","businessUnit":"412","gpsCode":"a3f09c1e"}
//	404 unknown account
package directory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Account is the directory view of a user.
type Account struct {
	Email        string `json:"email"`
	Status       string `json:"status"`       // OK | KO
	Site         string `json:"site"`         // attachment site trigram
	BusinessUnit string `json:"businessUnit"` // 3 digits
	GPSCode      string `json:"gpsCode"`      // 8 hexadecimal characters
}

// Client looks accounts up.
type Client interface {
	Lookup(ctx context.Context, email string) (Account, error)
}

// ErrNotFound is returned for unknown accounts (functional KO, not an error
// of the directory itself).
var ErrNotFound = notFoundError{}

type notFoundError struct{}

func (notFoundError) Error() string  { return "account not found in the enterprise directory" }
func (notFoundError) NotFound() bool { return true }

// HTTPError is a non-2xx answer of the directory.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string   { return fmt.Sprintf("directory: HTTP %d: %s", e.Code, e.Body) }
func (e *HTTPError) StatusCode() int { return e.Code }

// --- HTTP client ---------------------------------------------------------------

// HTTPClient calls a directory exposing the contract above.
type HTTPClient struct {
	base   string
	apiKey string
	http   *http.Client
}

// NewHTTPClient builds a client. The timeout is enforced by the caller's
// context; the http.Client timeout is only a safety net.
func NewHTTPClient(base, apiKey string) *HTTPClient {
	return &HTTPClient{base: base, apiKey: apiKey, http: &http.Client{Timeout: 30 * time.Second}}
}

// Lookup implements Client.
func (c *HTTPClient) Lookup(ctx context.Context, email string) (Account, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/accounts?email="+url.QueryEscape(email), nil)
	if err != nil {
		return Account{}, err
	}
	req.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Account{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Account{}, ErrNotFound
	case resp.StatusCode >= 300:
		return Account{}, &HTTPError{Code: resp.StatusCode, Body: string(body)}
	}
	var acc Account
	if err := json.Unmarshal(body, &acc); err != nil {
		return Account{}, fmt.Errorf("directory: invalid JSON answer: %w", err)
	}
	return acc, nil
}

// --- Instrumentation -----------------------------------------------------------

type instrumented struct {
	next Client
	rec  *telemetry.Recorder
}

// Instrument wraps a Client with telemetry.
func Instrument(next Client, rec *telemetry.Recorder) Client {
	return &instrumented{next: next, rec: rec}
}

func (i *instrumented) Lookup(ctx context.Context, email string) (acc Account, err error) {
	err = i.rec.Observe(ctx, telemetry.TargetDirectory, "lookup", func(ctx context.Context) error {
		acc, err = i.next.Lookup(ctx, email)
		return err
	})
	return
}

// IsNotFound reports whether err means "unknown account".
func IsNotFound(err error) bool { return errors.Is(err, ErrNotFound) }
