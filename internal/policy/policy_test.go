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
	p, _ := Parse([]byte(`eligibility: {allowed_gps_codes: ["A3F09C1E"], allowed_business_units: ["412"]}`))
	e := p.Eligibility
	cases := []struct {
		status, bu, gps string
		ok              bool
	}{
		{"OK", "412", "a3f09c1e", true}, // case-insensitive GPS match
		{"KO", "412", "a3f09c1e", false},
		{"OK", "412", "", false},
		{"OK", "412", "zz", false},
		{"OK", "412", "00000000", false},
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
