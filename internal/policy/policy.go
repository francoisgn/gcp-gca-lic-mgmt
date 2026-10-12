// Package policy loads the business rules that are expected to change without
// code changes: allowed durations, eligibility rules applied to the
// enterprise directory answer, the questions of the "usage details" step and
// the links displayed in the UI and e-mails. File: config/policy.yaml.
package policy

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// DurationOption is one selectable license duration.
type DurationOption struct {
	Code  domain.Duration `yaml:"code" json:"code"`
	Label string          `yaml:"label" json:"label"`
}

// Question is one field of the "usage details" step. Questions are rendered
// one after the other: answering a question unlocks the next one.
type Question struct {
	ID        string   `yaml:"id" json:"id"`
	Label     string   `yaml:"label" json:"label"`
	Help      string   `yaml:"help" json:"help,omitempty"`
	Type      string   `yaml:"type" json:"type"` // select | text | textarea
	Options   []string `yaml:"options" json:"options,omitempty"`
	Required  bool     `yaml:"required" json:"required"`
	MinLength int      `yaml:"min_length" json:"minLength,omitempty"`
	MaxLength int      `yaml:"max_length" json:"maxLength,omitempty"`
}

// Eligibility drives the automatic approval after the directory lookup.
type Eligibility struct {
	// AccountStatuses accepted as "account OK" (default: ["OK"]).
	AccountStatuses []string `yaml:"account_statuses"`
	// GPSCodePattern is the expected format of the GPS code.
	GPSCodePattern string `yaml:"gps_code_pattern"`
	// AllowedGPSCodes restricts auto-approval to a list of GPS codes.
	// Empty means "any GPS code matching the pattern".
	AllowedGPSCodes []string `yaml:"allowed_gps_codes"`
	// AllowedBusinessUnits restricts auto-approval to some business units.
	// Empty means "any business unit".
	AllowedBusinessUnits []string `yaml:"allowed_business_units"`

	gpsRe *regexp.Regexp
}

// Links are external URLs displayed in the UI and in e-mails.
type Links struct {
	Training        string `yaml:"training"`          // Gemini Code Assist training
	AIUsageTraining string `yaml:"ai_usage_training"` // company responsible-AI training
	VSCodeGuide     string `yaml:"vscode_guide"`
	UsagePolicy     string `yaml:"usage_policy"`
	Support         string `yaml:"support"`
}

// Policy is the parsed policy file.
type Policy struct {
	Durations         []DurationOption `yaml:"durations"`
	DefaultDuration   domain.Duration  `yaml:"default_duration"`
	UnusedDays        int              `yaml:"unused_days"`
	BufferMaxAttempts int              `yaml:"buffer_max_attempts"`
	Eligibility       Eligibility      `yaml:"eligibility"`
	Questions         []Question       `yaml:"questions"`
	Links             Links            `yaml:"links"`
}

// Load reads and validates the policy file.
func Load(path string) (*Policy, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read policy file: %w", err)
	}
	return Parse(raw)
}

// Parse builds a Policy from YAML content and applies defaults.
func Parse(raw []byte) (*Policy, error) {
	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("parse policy file: %w", err)
	}
	if len(p.Durations) == 0 {
		for _, c := range []string{"7d", "1M", "3M", "6M", "1Y"} {
			p.Durations = append(p.Durations, DurationOption{Code: domain.Duration(c)})
		}
	}
	for i, d := range p.Durations {
		if _, err := domain.ParseDuration(string(d.Code)); err != nil {
			return nil, err
		}
		if d.Label == "" {
			p.Durations[i].Label = d.Code.Label()
		}
	}
	if p.DefaultDuration == "" {
		p.DefaultDuration = p.Durations[0].Code
	}
	if p.UnusedDays <= 0 {
		p.UnusedDays = 7
	}
	if p.BufferMaxAttempts <= 0 {
		p.BufferMaxAttempts = 30
	}
	if len(p.Eligibility.AccountStatuses) == 0 {
		p.Eligibility.AccountStatuses = []string{"OK"}
	}
	if p.Eligibility.GPSCodePattern == "" {
		p.Eligibility.GPSCodePattern = `^[0-9a-fA-F]{8}$`
	}
	var err error
	if p.Eligibility.gpsRe, err = regexp.Compile(p.Eligibility.GPSCodePattern); err != nil {
		return nil, fmt.Errorf("eligibility.gps_code_pattern: %w", err)
	}
	seen := map[string]bool{}
	for _, q := range p.Questions {
		if q.ID == "" || seen[q.ID] {
			return nil, fmt.Errorf("question ids must be unique and non-empty (got %q)", q.ID)
		}
		seen[q.ID] = true
		switch q.Type {
		case "select", "text", "textarea":
		default:
			return nil, fmt.Errorf("question %q: unsupported type %q", q.ID, q.Type)
		}
	}
	return &p, nil
}

// AllowsDuration reports whether a duration code is offered.
func (p *Policy) AllowsDuration(d domain.Duration) bool {
	for _, o := range p.Durations {
		if o.Code == d {
			return true
		}
	}
	return false
}

// ValidateAnswers checks the "usage details" answers against the questions.
// It returns the cleaned answers (unknown keys dropped, values trimmed).
func (p *Policy) ValidateAnswers(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, q := range p.Questions {
		v := strings.TrimSpace(in[q.ID])
		if v == "" {
			if q.Required {
				return nil, fmt.Errorf("%q is required", q.Label)
			}
			continue
		}
		if q.Type == "select" && !slices.Contains(q.Options, v) {
			return nil, fmt.Errorf("%q: invalid option", q.Label)
		}
		if q.MinLength > 0 && len([]rune(v)) < q.MinLength {
			return nil, fmt.Errorf("%q must be at least %d characters", q.Label, q.MinLength)
		}
		if q.MaxLength > 0 && len([]rune(v)) > q.MaxLength {
			return nil, fmt.Errorf("%q must be at most %d characters", q.Label, q.MaxLength)
		}
		out[q.ID] = v
	}
	return out, nil
}

// Evaluate applies the eligibility rules to a directory answer. It returns
// ok=false and an English reason when the request cannot be auto-approved.
func (e *Eligibility) Evaluate(accountStatus, businessUnit, gpsCode string) (ok bool, reason string) {
	if !slices.Contains(e.AccountStatuses, accountStatus) {
		return false, fmt.Sprintf("Your directory account status is %q.", accountStatus)
	}
	if strings.TrimSpace(gpsCode) == "" {
		return false, "No GPS code is set on your directory profile."
	}
	if e.gpsRe != nil && !e.gpsRe.MatchString(gpsCode) {
		return false, "The GPS code on your directory profile has an unexpected format."
	}
	if len(e.AllowedGPSCodes) > 0 && !containsFold(e.AllowedGPSCodes, gpsCode) {
		return false, "Your GPS code is not eligible for automatic approval."
	}
	if len(e.AllowedBusinessUnits) > 0 && !slices.Contains(e.AllowedBusinessUnits, businessUnit) {
		return false, "Your business unit is not eligible for automatic approval."
	}
	return true, ""
}

func containsFold(list []string, v string) bool {
	for _, s := range list {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}
