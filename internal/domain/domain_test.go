package domain

import (
	"testing"
	"time"
)

func TestDurations(t *testing.T) {
	base := time.Date(2026, 1, 31, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		in    string
		want  time.Time
		label string
	}{
		{"7d", base.AddDate(0, 0, 7), "7 days"},
		{"1M", base.AddDate(0, 1, 0), "1 month"},
		{"3M", base.AddDate(0, 3, 0), "3 months"},
		{"1Y", base.AddDate(1, 0, 0), "1 year"},
		{"2w", base.AddDate(0, 0, 14), "2 weeks"},
	}
	for _, c := range cases {
		d, err := ParseDuration(c.in)
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if got := d.AddTo(base); !got.Equal(c.want) {
			t.Errorf("%s: AddTo = %v, want %v", c.in, got, c.want)
		}
		if d.Label() != c.label {
			t.Errorf("%s: label %q, want %q", c.in, d.Label(), c.label)
		}
	}
	for _, bad := range []string{"", "M", "0d", "-1M", "3x", "abc"} {
		if _, err := ParseDuration(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestSteps(t *testing.T) {
	s := NewSteps()
	if len(s) != 9 {
		t.Fatalf("steps = %d", len(s))
	}
	for _, st := range s {
		want := StepTodo
		if st.ID == StepAdmin {
			want = StepSkipped // greyed unless an escalation happens
		}
		if st.State != want {
			t.Errorf("%s = %s, want %s", st.ID, st.State, want)
		}
	}
	SetStep(s, StepLicense, StepWarn, "timeout")
	if s[7].State != StepWarn || s[7].Hint != "timeout" {
		t.Fatalf("SetStep: %+v", s[7])
	}
	// NewSteps returns independent copies.
	if NewSteps()[7].State != StepTodo {
		t.Fatal("NewSteps shares state")
	}
}

func TestStatusHelpers(t *testing.T) {
	if !ReqQueued.IsOpen() || ReqAssigned.IsOpen() || ReqRejected.IsOpen() {
		t.Fatal("IsOpen")
	}
	if NormalizeEmail("  Jane.Doe@Example.COM ") != "jane.doe@example.com" {
		t.Fatal("NormalizeEmail")
	}
	if EndUnused.Label() != "Unused" || EndAdminRemoval.Label() != "Admin removal" {
		t.Fatal("labels")
	}
}
