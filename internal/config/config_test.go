package config

import "testing"

// The settings files of the repository must parse strictly.
func TestRepositorySettingsFiles(t *testing.T) {
	for _, f := range []string{"../../config/settings.yaml", "../../config/settings.dev.yaml"} {
		s, err := LoadSettings(f)
		if err != nil {
			t.Fatalf("%s: %v", f, err)
		}
		if s.Directory.Fields.GPSCode == "" || s.Tracker.Mode == "" || s.Mail.From == "" {
			t.Fatalf("%s: incomplete settings %+v", f, s)
		}
	}
}

// Environment variables override the settings file; secrets come from the
// variable named in the file.
func TestPrecedence(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("SETTINGS_FILE", "../../config/settings.yaml") // prod-like file
	t.Setenv("ROLES_FILE", "../../config/roles.yaml")
	t.Setenv("DIRECTORY_API_KEY", "from-secret-manager")
	t.Setenv("GITLAB_TOKEN", "test-token")
	t.Setenv("SMTP_PASSWORD", "x")
	t.Setenv("TRACKER_MODE", "log") // env override of the file value (gitlab)
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.DirectoryMode != "http" || c.DirectoryURL != "https://directory.example.com/api" || c.DirectoryAPIKey != "from-secret-manager" {
		t.Fatalf("directory = %s %s key=%q", c.DirectoryMode, c.DirectoryURL, c.DirectoryAPIKey)
	}
	if c.TrackerMode != "log" || c.GitLabProject != "platform/gca-license-portal" || c.GitLabToken != "test-token" {
		t.Fatalf("tracker = %s %s", c.TrackerMode, c.GitLabProject)
	}
	if c.DirectoryFields.GPSCode != "gpsCode" || c.DirectoryTimeout.String() != "5s" {
		t.Fatalf("fields = %+v timeout=%v", c.DirectoryFields, c.DirectoryTimeout)
	}
}

func TestMissingDirectoryKeyRejected(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("SETTINGS_FILE", "../../config/settings.yaml")
	t.Setenv("DIRECTORY_API_KEY", "")
	t.Setenv("TRACKER_MODE", "log")
	t.Setenv("MAIL_MODE", "log")
	if _, err := Load(); err == nil {
		t.Fatal("http directory without key accepted")
	}
}
