package config

import (
	"bytes"
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// Settings mirrors config/settings.yaml (production) and
// config/settings.dev.yaml (local development): the NON-secret connection
// details of the integrations, versioned in git.
//
// Secrets never appear in these files. Each `*_secret_env` key names the
// environment variable holding the secret; on Cloud Run that variable is
// filled from Secret Manager (see deploy/terraform/secrets.tf).
//
// Precedence: environment variable > settings file > built-in default.
type Settings struct {
	Directory DirectorySettings `yaml:"directory"`
	Tracker   TrackerSettings   `yaml:"tracker"`
	Mail      MailSettings      `yaml:"mail"`
}

// DirectorySettings describes how to reach the enterprise directory.
type DirectorySettings struct {
	Mode    string        `yaml:"mode"`     // http | simulated
	BaseURL string        `yaml:"base_url"` // e.g. https://directory.example.com/api
	Timeout time.Duration `yaml:"timeout"`  // per lookup when a user waits
	Lookup  struct {
		// Path of the lookup. "{email}" is replaced by the escaped e-mail
		// (/v1/accounts/{email}); otherwise the e-mail goes in EmailParam.
		Path       string `yaml:"path"`
		EmailParam string `yaml:"email_param"`
	} `yaml:"lookup"`
	Auth struct {
		Type      string `yaml:"type"`       // bearer | header | none
		Header    string `yaml:"header"`     // header name when type = header (e.g. X-API-Key)
		SecretEnv string `yaml:"secret_env"` // env var holding the key (from Secret Manager)
	} `yaml:"auth"`
	// Fields maps the JSON answer of the directory to the portal model.
	// Dotted paths reach nested objects ("profile.gpsCode").
	Fields    DirectoryFields `yaml:"fields"`
	Simulator struct {
		FailureRate float64       `yaml:"failure_rate"`
		MaxLatency  time.Duration `yaml:"max_latency"`
	} `yaml:"simulator"`
}

// DirectoryFields are the JSON field names of a directory answer.
type DirectoryFields struct {
	Status       string `yaml:"status"`
	Site         string `yaml:"site"`
	BusinessUnit string `yaml:"business_unit"`
	GPSCode      string `yaml:"gps_code"`
}

// TrackerSettings describes where user feedback is forwarded.
type TrackerSettings struct {
	Mode   string `yaml:"mode"` // gitlab | jira | log
	GitLab struct {
		BaseURL        string   `yaml:"base_url"`
		Project        string   `yaml:"project"` // numeric id or group/project
		Labels         []string `yaml:"labels"`
		TokenSecretEnv string   `yaml:"token_secret_env"`
	} `yaml:"gitlab"`
	Jira struct {
		BaseURL        string   `yaml:"base_url"`
		User           string   `yaml:"user"`
		Project        string   `yaml:"project"`
		IssueType      string   `yaml:"issue_type"`
		Labels         []string `yaml:"labels"`
		TokenSecretEnv string   `yaml:"token_secret_env"`
	} `yaml:"jira"`
}

// MailSettings describes the e-mail transport.
type MailSettings struct {
	Mode string `yaml:"mode"` // smtp | log
	From string `yaml:"from"`
	SMTP struct {
		Host              string `yaml:"host"`
		Port              int    `yaml:"port"`
		User              string `yaml:"user"`
		PasswordSecretEnv string `yaml:"password_secret_env"`
	} `yaml:"smtp"`
}

// LoadSettings parses a settings file (strict: unknown keys are errors). A
// missing file yields empty settings (built-in defaults apply).
func LoadSettings(path string) (*Settings, error) {
	s := &Settings{}
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read settings file: %w", err)
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(s); err != nil {
		return nil, fmt.Errorf("parse settings file %s: %w", path, err)
	}
	return s, nil
}

// or returns the first non-empty string.
func or(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// orDuration returns v when set, def otherwise.
func orDuration(v, def time.Duration) time.Duration {
	if v > 0 {
		return v
	}
	return def
}
