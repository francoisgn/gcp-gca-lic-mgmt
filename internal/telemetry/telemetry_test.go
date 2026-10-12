package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"google.golang.org/api/googleapi"
)

type httpErr int

func (e httpErr) Error() string   { return fmt.Sprintf("http %d", int(e)) }
func (e httpErr) StatusCode() int { return int(e) }

func TestClassify(t *testing.T) {
	cases := []struct {
		err       error
		want      string
		transient bool
	}{
		{nil, OutcomeOK, false},
		{context.DeadlineExceeded, OutcomeTimeout, true},
		{fmt.Errorf("wrapped: %w", context.DeadlineExceeded), OutcomeTimeout, true},
		{httpErr(503), OutcomeHTTP5xx, true},
		{httpErr(429), OutcomeRateLimited, true},
		{httpErr(404), OutcomeNotFound, false},
		{httpErr(403), OutcomeHTTP4xx, false},
		{&googleapi.Error{Code: 504}, OutcomeTimeout, true},
		{&url.Error{Op: "Get", URL: "x", Err: errors.New("connection refused")}, OutcomeNetwork, true},
		{errors.New("boom"), OutcomeError, false},
	}
	for _, c := range cases {
		if got := Classify(c.err); got != c.want {
			t.Errorf("Classify(%v) = %s, want %s", c.err, got, c.want)
		}
		if IsTransient(c.err) != c.transient {
			t.Errorf("IsTransient(%v) = %v", c.err, !c.transient)
		}
	}
}

func TestKind(t *testing.T) {
	ctx := context.Background()
	if KindFrom(ctx) != KindJob || KindFromOr(ctx, KindUX) != KindUX {
		t.Fatal("defaults")
	}
	if KindFrom(WithKind(ctx, KindAdmin)) != KindAdmin {
		t.Fatal("WithKind")
	}
}
