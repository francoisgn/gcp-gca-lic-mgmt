package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/jobs"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/testenv"
)

// newServer wires the HTTP server on a seeded demo database.
func newServer(t *testing.T, opts ...testenv.Option) (*httptest.Server, *testenv.Env) {
	t.Helper()
	e := testenv.New(t, 12, opts...)
	if err := e.Svc.SeedDemo(context.Background()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	srv, err := New(e.Cfg, e.Svc, jobs.NewRunner(e.DB), e.Roles, auth.Dev{DefaultUser: "jane.doe@example.com"}, "test")
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, e
}

func get(t *testing.T, ts *httptest.Server, user, path string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	req.AddCookie(&http.Cookie{Name: auth.DevCookie, Value: user})
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

// Every demo identity gets its landing page, with the expected state.
func TestHomeStates(t *testing.T) {
	ts, _ := newServer(t)
	expect := map[string]string{
		"olivia.brown@example.com": "Your license is active",
		"noah.wilson@example.com":  "you are in the waiting list",
		"mia.ko@example.com":       "Waiting for an administrator",
		"liam.ko@example.com":      "Your last request was rejected",
		"sofia.edslow@example.com": "being processed with priority",
		"jane.doe@example.com":     "Pool exhausted",
		"paul.new@example.com":     "Request a license",
	}
	for _, u := range devUsers {
		code, body := get(t, ts, u.Email, "/")
		if code != http.StatusOK {
			t.Fatalf("%s: HTTP %d", u.Email, code)
		}
		if want, ok := expect[u.Email]; ok && !strings.Contains(body, want) {
			t.Errorf("%s: page does not contain %q", u.Email, want)
		}
	}
	// Previous license history is shown when not licensed (jane had one).
	if _, body := get(t, ts, "jane.doe@example.com", "/"); !strings.Contains(body, "Previous licenses") {
		t.Error("history missing")
	}
}

func TestPagesAndAuthorization(t *testing.T) {
	ts, _ := newServer(t)
	for _, c := range []struct {
		user, path string
		want       int
	}{
		{"jane.doe@example.com", "/feedback", 200},
		{"jane.doe@example.com", "/admin", 403},
		{"jane.doe@example.com", "/observability", 403},
		{"jane.doe@example.com", "/api/obs/summary", 403},
		{"alex.martin@example.com", "/admin", 200},
		{"alex.martin@example.com", "/observability", 200},
		{"alex.martin@example.com", "/api/obs/flows?days=30", 200},
		{"alex.martin@example.com", "/api/obs/tech", 200},
		{"alex.martin@example.com", "/api/obs/depths", 200},
		{"alex.martin@example.com", "/api/obs/deviations", 200},
		{"super.admin@example.com", "/observability", 200},
		{"super.admin@example.com", "/static/css/app.css", 200},
	} {
		if code, _ := get(t, ts, c.user, c.path); code != c.want {
			t.Errorf("%s %s: HTTP %d, want %d", c.user, c.path, code, c.want)
		}
	}
	// Super admin actions are refused to admins.
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/super/refresh", nil)
	req.AddCookie(&http.Cookie{Name: auth.DevCookie, Value: "alex.martin@example.com"})
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != http.StatusForbidden {
		t.Errorf("admin forced a refresh: HTTP %d", resp.StatusCode)
	}
}

// Cross-site POSTs are rejected (CSRF), same-origin ones go through.
func TestCrossOriginProtection(t *testing.T) {
	ts, _ := newServer(t)
	post := func(site string) int {
		req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/queue/leave", strings.NewReader("{}"))
		req.AddCookie(&http.Cookie{Name: auth.DevCookie, Value: "noah.wilson@example.com"})
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Sec-Fetch-Site", site)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if code := post("cross-site"); code != http.StatusForbidden {
		t.Fatalf("cross-site POST: HTTP %d", code)
	}
	if code := post("same-origin"); code != http.StatusOK {
		t.Fatalf("same-origin POST: HTTP %d", code)
	}
}

func TestRateLimit(t *testing.T) {
	ts, _ := newServer(t, func(c *config.Config) { c.RateLimitPerMin, c.RateLimitBurst = 1, 3 })
	codes := map[int]int{}
	for i := 0; i < 6; i++ {
		code, _ := get(t, ts, "jane.doe@example.com", "/api/counter")
		codes[code]++
	}
	if codes[http.StatusTooManyRequests] == 0 {
		t.Fatalf("no 429 after burst: %v", codes)
	}
}

func TestSecurityHeaders(t *testing.T) {
	ts, _ := newServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "script-src 'self'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("CSP = %q", csp)
	}
	if resp.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("nosniff missing")
	}
}
