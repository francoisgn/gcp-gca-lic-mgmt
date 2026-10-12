// Package tracker forwards user feedback to an issue tracker.
//
// Users do not all have a tracker account, so issues are created with a
// technical account (Jira API token / GitLab project access token stored in
// Secret Manager). The real requester is identified by the e-mail coming from
// SSO, written in the issue body and as a label — never trusted from the form.
package tracker

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// Issue is a feedback to forward.
type Issue struct {
	Category  string // IMPROVEMENT | BUG | QUESTION
	Requester string // SSO e-mail
	Message   string
	PageURL   string
}

// Summary returns a one-line issue title.
func (i Issue) Summary() string {
	msg := strings.Join(strings.Fields(i.Message), " ")
	if r := []rune(msg); len(r) > 70 {
		msg = string(r[:70]) + "…"
	}
	return fmt.Sprintf("[GCA portal][%s] %s", strings.ToLower(i.Category), msg)
}

// Body returns the issue description.
func (i Issue) Body() string {
	return fmt.Sprintf("%s\n\n---\nRequester (SSO): %s\nCategory: %s\nSubmitted from: %s\nSubmitted at: %s",
		i.Message, i.Requester, i.Category, i.PageURL, time.Now().UTC().Format(time.RFC3339))
}

// Tracker creates issues.
type Tracker interface {
	Create(ctx context.Context, i Issue) (key, link string, err error)
}

// HTTPError is a non-2xx answer of a tracker API.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string   { return fmt.Sprintf("tracker: HTTP %d: %s", e.Code, e.Body) }
func (e *HTTPError) StatusCode() int { return e.Code }

// --- Log -----------------------------------------------------------------------

// Log only logs the feedback (development).
type Log struct{}

// Create implements Tracker.
func (Log) Create(_ context.Context, i Issue) (string, string, error) {
	slog.Info("feedback (log tracker)", "summary", i.Summary(), "requester", i.Requester)
	return "", "", nil
}

// --- Jira Cloud ------------------------------------------------------------------

// Jira creates issues with the Jira Cloud REST API v3.
type Jira struct {
	BaseURL   string // https://your-domain.atlassian.net
	User      string // technical account e-mail
	Token     string // API token
	Project   string // project key
	IssueType string // e.g. Task
	HTTP      *http.Client
}

// Create implements Tracker.
func (j Jira) Create(ctx context.Context, i Issue) (string, string, error) {
	// Atlassian Document Format: one paragraph per line.
	var content []map[string]any
	for _, line := range strings.Split(i.Body(), "\n") {
		p := map[string]any{"type": "paragraph"}
		if line != "" {
			p["content"] = []map[string]any{{"type": "text", "text": line}}
		}
		content = append(content, p)
	}
	payload := map[string]any{"fields": map[string]any{
		"project":     map[string]string{"key": j.Project},
		"issuetype":   map[string]string{"name": j.IssueType},
		"summary":     i.Summary(),
		"description": map[string]any{"type": "doc", "version": 1, "content": content},
		"labels":      []string{"gca-portal", "feedback-" + strings.ToLower(i.Category)},
	}}
	var out struct {
		Key string `json:"key"`
	}
	headers := map[string]string{"Authorization": "Basic " + basic(j.User, j.Token)}
	if err := postJSON(ctx, j.HTTP, j.BaseURL+"/rest/api/3/issue", headers, payload, &out); err != nil {
		return "", "", err
	}
	return out.Key, j.BaseURL + "/browse/" + out.Key, nil
}

// --- GitLab ----------------------------------------------------------------------

// GitLab creates issues with the GitLab REST API v4.
type GitLab struct {
	BaseURL string // https://gitlab.example.com
	Token   string // project access token (scope: api)
	Project string // numeric id or "group/project"
	HTTP    *http.Client
}

// Create implements Tracker.
func (g GitLab) Create(ctx context.Context, i Issue) (string, string, error) {
	payload := map[string]any{
		"title":       i.Summary(),
		"description": i.Body(),
		"labels":      "gca-portal,feedback::" + strings.ToLower(i.Category),
	}
	var out struct {
		IID    int    `json:"iid"`
		WebURL string `json:"web_url"`
	}
	endpoint := fmt.Sprintf("%s/api/v4/projects/%s/issues", g.BaseURL, url.PathEscape(g.Project))
	if err := postJSON(ctx, g.HTTP, endpoint, map[string]string{"PRIVATE-TOKEN": g.Token}, payload, &out); err != nil {
		return "", "", err
	}
	return fmt.Sprintf("#%d", out.IID), out.WebURL, nil
}

// --- Instrumentation -----------------------------------------------------------

type instrumented struct {
	next Tracker
	rec  *telemetry.Recorder
}

// Instrument wraps a Tracker with telemetry.
func Instrument(next Tracker, rec *telemetry.Recorder) Tracker {
	return &instrumented{next: next, rec: rec}
}

func (t *instrumented) Create(ctx context.Context, i Issue) (key, link string, err error) {
	err = t.rec.Observe(ctx, telemetry.TargetTracker, "createIssue", func(ctx context.Context) error {
		key, link, err = t.next.Create(ctx, i)
		return err
	})
	return
}

// --- helpers -------------------------------------------------------------------

func postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, in, out any) error {
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	body, err := json.Marshal(in)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode >= 300 {
		return &HTTPError{Code: resp.StatusCode, Body: string(raw)}
	}
	return json.Unmarshal(raw, out)
}

func basic(user, token string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + token))
}
