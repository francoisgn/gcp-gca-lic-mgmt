// Package telemetry measures every outbound call (enterprise directory,
// Google APIs, tracker, mail). Each call is:
//   - timed and classified (OK, TIMEOUT, HTTP_4XX, HTTP_5XX, NETWORK...);
//   - written to the api_calls table (technical KPIs, 10 days retention);
//   - logged as a structured JSON line (Cloud Logging, log-based metrics).
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/url"
	"time"

	"github.com/googleapis/gax-go/v2/apierror"
	"google.golang.org/api/googleapi"
	"google.golang.org/grpc/codes"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
)

// Targets of outbound calls.
const (
	TargetDirectory   = "DIRECTORY"
	TargetLicense     = "GOOGLE_LICENSE"     // license pool: getPool / enumerate / assign / unassign
	TargetOrders      = "GOOGLE_ORDERS"      // subscriptions (orders) listing
	TargetCredentials = "GOOGLE_CREDENTIALS" // IAM bundle: group membership grant / revoke
	TargetTracker     = "TRACKER"
	TargetMail        = "MAIL"
)

// Outcomes of outbound calls.
const (
	OutcomeOK          = "OK"
	OutcomeTimeout     = "TIMEOUT"
	OutcomeRateLimited = "RATE_LIMITED"
	OutcomeHTTP4xx     = "HTTP_4XX"
	OutcomeHTTP5xx     = "HTTP_5XX"
	OutcomeNetwork     = "NETWORK"
	OutcomeNotFound    = "NOT_FOUND"
	OutcomeError       = "ERROR"
)

// Kind is the execution context of a call: did a user wait for it?
type Kind string

const (
	KindUX    Kind = "UX"    // a user is waiting in front of the screen
	KindJob   Kind = "JOB"   // scheduled job (buffer retry, purge, refresh...)
	KindAdmin Kind = "ADMIN" // administrator action
)

type kindKey struct{}

// WithKind tags a context with an execution kind.
func WithKind(ctx context.Context, k Kind) context.Context {
	return context.WithValue(ctx, kindKey{}, k)
}

// KindFrom returns the execution kind of a context (JOB by default).
func KindFrom(ctx context.Context) Kind {
	if k, ok := ctx.Value(kindKey{}).(Kind); ok {
		return k
	}
	return KindJob
}

// KindFromOr returns the execution kind of a context, or def when unset.
func KindFromOr(ctx context.Context, def Kind) Kind {
	if k, ok := ctx.Value(kindKey{}).(Kind); ok {
		return k
	}
	return def
}

// StatusCoder is implemented by HTTP errors of our own clients.
type StatusCoder interface{ StatusCode() int }

// NotFounder is implemented by "not found" errors of our own clients.
type NotFounder interface{ NotFound() bool }

// Sink persists call records (implemented by store.Q).
type Sink interface {
	InsertAPICall(ctx context.Context, c store.APICall) error
}

// Recorder measures and records outbound calls.
type Recorder struct {
	sink Sink
}

// NewRecorder builds a recorder. sink may be nil (records are only logged).
func NewRecorder(sink Sink) *Recorder { return &Recorder{sink: sink} }

// Observe runs fn, measures it and records the outcome. The error of fn is
// returned unchanged.
func (r *Recorder) Observe(ctx context.Context, target, operation string, fn func(context.Context) error) error {
	start := time.Now()
	err := fn(ctx)
	r.Record(ctx, target, operation, time.Since(start), err)
	return err
}

// Record stores a call measured by the caller.
func (r *Recorder) Record(ctx context.Context, target, operation string, d time.Duration, err error) {
	outcome := Classify(err)
	kind := KindFrom(ctx)
	attrs := []any{"target", target, "operation", operation, "context", string(kind),
		"duration_ms", d.Milliseconds(), "outcome", outcome}
	var detail *string
	if err != nil {
		msg := err.Error()
		if len(msg) > 300 {
			msg = msg[:300]
		}
		detail = &msg
		attrs = append(attrs, "err", msg)
		slog.Warn("outbound call failed", attrs...)
	} else {
		slog.Debug("outbound call", attrs...)
	}
	if r == nil || r.sink == nil {
		return
	}
	// The caller's context may already be expired (that is precisely the
	// timeout case we want to record), so use a detached short context.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if werr := r.sink.InsertAPICall(wctx, store.APICall{
		TS: time.Now(), Target: target, Operation: operation, Context: string(kind),
		DurationMS: int(d.Milliseconds()), Outcome: outcome, Detail: detail,
	}); werr != nil {
		slog.Error("cannot record api call", "err", werr)
	}
}

// Classify maps an error to an outcome.
func Classify(err error) string {
	if err == nil {
		return OutcomeOK
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return OutcomeTimeout
	}
	var nf NotFounder
	if errors.As(err, &nf) && nf.NotFound() {
		return OutcomeNotFound
	}
	if code := httpCode(err); code > 0 {
		return classifyHTTP(code)
	}
	var apiErr *apierror.APIError
	if errors.As(err, &apiErr) && apiErr.GRPCStatus() != nil {
		switch apiErr.GRPCStatus().Code() {
		case codes.DeadlineExceeded:
			return OutcomeTimeout
		case codes.NotFound:
			return OutcomeNotFound
		case codes.ResourceExhausted:
			return OutcomeRateLimited
		case codes.Unavailable, codes.Internal, codes.Unknown:
			return OutcomeHTTP5xx
		default:
			return OutcomeHTTP4xx
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return OutcomeTimeout
		}
		return OutcomeNetwork
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return OutcomeNetwork
	}
	return OutcomeError
}

// httpCode extracts an HTTP status code from known error types.
func httpCode(err error) int {
	var sc StatusCoder
	if errors.As(err, &sc) {
		return sc.StatusCode()
	}
	var gErr *googleapi.Error
	if errors.As(err, &gErr) {
		return gErr.Code
	}
	var apiErr *apierror.APIError
	if errors.As(err, &apiErr) && apiErr.HTTPCode() > 0 {
		return apiErr.HTTPCode()
	}
	return 0
}

func classifyHTTP(code int) string {
	switch {
	case code == 404:
		return OutcomeNotFound
	case code == 408 || code == 504:
		return OutcomeTimeout
	case code == 429:
		return OutcomeRateLimited
	case code >= 500:
		return OutcomeHTTP5xx
	case code >= 400:
		return OutcomeHTTP4xx
	}
	return OutcomeOK
}

// IsTransient reports whether an error is worth retrying later (buffer):
// timeouts, rate limiting, 5xx and network errors.
func IsTransient(err error) bool {
	switch Classify(err) {
	case OutcomeTimeout, OutcomeRateLimited, OutcomeHTTP5xx, OutcomeNetwork:
		return true
	}
	return false
}

// IsTimeout reports whether an error is a timeout.
func IsTimeout(err error) bool { return Classify(err) == OutcomeTimeout }
