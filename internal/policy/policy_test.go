package policy

import (
	"os"
	"testing"
)

func TestRepositoryPolicyFileIsValid(t *testing.T) {
	raw, err := os.ReadFile("../../config/policy.yaml")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Durations) != 5 || p.UnusedDays != 7 || !p.AllowsDuration("1Y") || p.AllowsDuration("2Y") {
		t.Fatalf("unexpected policy: %+v", p)
	}
}

func TestDefaultsAndValidation(t *testing.T) {
	p, err := Parse([]byte(`questions: [{id: q, label: Q, type: textarea, required: true, min_length: 5, max_length: 10}]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Durations) != 5 || p.UnusedDays != 7 || p.BufferMaxAttempts != 30 {
		t.Fatalf("defaults not applied: %+v", p)
	}
	if _, err := p.ValidateAnswers(map[string]string{}); err == nil {
		t.Error("missing required answer accepted")
	}
	if _, err := p.ValidateAnswers(map[string]string{"q": "abc"}); err == nil {
		t.Error("too short accepted")
	}
	if _, err := p.ValidateAnswers(map[string]string{"q": "abcdefghijk"}); err == nil {
		t.Error("too long accepted")
	}
	out, err := p.ValidateAnswers(map[string]string{"q": "  hello  ", "unknown": "x"})
	if err != nil || out["q"] != "hello" || len(out) != 1 {
		t.Fatalf("cleaned answers = %v, %v", out, err)
	}
	if _, err := Parse([]byte(`questions: [{id: a, label: A, type: radio}]`)); err == nil {
		t.Error("unsupported question type accepted")
	}
	if _, err := Parse([]byte(`questions: [{id: a, label: A, type: text}, {id: a, label: B, type: text}]`)); err == nil {
		t.Error("duplicate question id accepted")
	}
}

func TestEligibility(t *testing.T) {
	p, err := Parse([]byte(`eligibility: {allowed_business_units: ["412"]}`))
	if err != nil {
		t.Fatal(err)
	}
	e := p.Eligibility
	// The GPS allowlist is a database table (checked by the service).
	cases := []struct {
		status, bu, gps string
		ok              bool
	}{
		{"OK", "412", "a3f09c1e", true},
		{"KO", "412", "a3f09c1e", false},
		{"OK", "412", "", false},
		{"OK", "412", "zz", false},
		{"OK", "999", "a3f09c1e", false},
	}
	for _, c := range cases {
		if ok, reason := e.Evaluate(c.status, c.bu, c.gps); ok != c.ok || (!ok && reason == "") {
			t.Errorf("%+v: ok=%v reason=%q", c, ok, reason)
		}
	}
	open, _ := Parse([]byte(`{}`))
	if ok, _ := open.Eligibility.Evaluate("OK", "123", "deadbeef"); !ok {
		t.Error("open policy should accept any valid GPS code")
	}
}

func TestExternalAccountsAndNotifications(t *testing.T) {
	p, err := Parse([]byte("accounts: {external_suffix: \".ext\"}\nnotifications: {buffered: false}"))
	if err != nil {
		t.Fatal(err)
	}
	for email, want := range map[string]bool{
		"jane.doe.ext@example.com": true, "Jane.Doe.EXT@example.com": true,
		"jane.doe@example.com": false, "jane.extern@example.com": false, "ext@example.com": false,
	} {
		if p.IsExternal(email) != want {
			t.Errorf("IsExternal(%s) != %v", email, want)
		}
	}
	if p.Notify("buffered") || !p.Notify("license_assigned") {
		t.Fatal("Notify")
	}
	if _, err := Parse([]byte("unknown_key: 1")); err == nil {
		t.Fatal("unknown key accepted")
	}
}
