package web

import (
	"bytes"
	"fmt"
	"html/template"
	"log/slog"
	"math"
	"net/http"
	"reflect"
	"strings"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/config"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
)

// pageTemplate is a page parsed together with the layout and the partials.
type pageTemplate struct{ t *template.Template }

// pageFiles lists the pages; each one is parsed with layout + partials.
var pageFiles = []string{"home", "admin", "observability", "feedback", "error"}

// Page is the data passed to every template.
type Page struct {
	Title      string
	Nav        string // active navigation entry
	User       auth.Identity
	Counter    service.PoolCounter
	Pending    int // escalations waiting (admins)
	Dev        bool
	DevUsers   []DevUser
	GoogleFake bool
	Version    string
	Year       int
	Data       any // page specific
	JSON       any // serialised into <script type="application/json" id="page-data">
}

// DevUser is a preset identity of the development switcher.
type DevUser struct {
	Email string
	Label string
}

// devUsers are the identities offered by the dev switcher. Markers in the
// e-mail drive the simulators (see internal/directory and internal/gcp).
var devUsers = []DevUser{
	{"super.admin@example.com", "Super admin"},
	{"alex.martin@example.com", "Admin"},
	{"jane.doe@example.com", "User — eligible (had a license)"},
	{"paul.new@example.com", "User — eligible, never requested"},
	{"olivia.brown@example.com", "User — active license"},
	{"noah.wilson@example.com", "User — in the waiting list"},
	{"mia.ko@example.com", "User — pending admin validation"},
	{"liam.ko@example.com", "User — rejected twice"},
	{"sofia.edslow@example.com", "User — in the buffer (directory timeout)"},
	{"john.ko@example.com", "User — directory KO (account)"},
	{"emma.nogps@example.com", "User — directory KO (no GPS code)"},
	{"tom.badgps@example.com", "User — GPS code not authorized"},
	{"marc.dupont.ext@example.com", "External — never auto-approved"},
	{"lisa.martin.ext@example.com", "External — pending admin validation"},
	{"lucas.edslow@example.com", "User — directory timeout"},
	{"nina.slow@example.com", "User — Google license timeout (1st try)"},
}

func parsePages(cfg *config.Config) (map[string]*pageTemplate, error) {
	funcs := templateFuncs(cfg.TimeZone)
	out := map[string]*pageTemplate{}
	for _, name := range pageFiles {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(templatesFS,
			"templates/layout.html", "templates/partials.html", "templates/"+name+".html")
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", name, err)
		}
		out[name] = &pageTemplate{t: t}
	}
	return out, nil
}

// render executes a page into a buffer first, so a template error never
// produces half a page.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, name string, p *Page) {
	if id, ok := auth.FromContext(r.Context()); ok {
		p.User = id
	}
	p.Dev = s.cfg.AuthMode == "dev"
	if p.Dev {
		p.DevUsers = devUsers
	}
	p.GoogleFake = s.cfg.GoogleMode == "fake"
	p.Version = s.version
	p.Year = time.Now().Year()
	if p.User.Email != "" {
		p.Counter = s.svc.Counter(r.Context())
		if p.User.Role.IsAdmin() {
			p.Pending = s.svc.PendingCount(r.Context())
		}
	}
	var buf bytes.Buffer
	if err := s.pages[name].t.Execute(&buf, p); err != nil {
		slog.Error("template execution", "page", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = buf.WriteTo(w)
}

// renderError renders the error page.
func (s *Server) renderError(w http.ResponseWriter, r *http.Request, status int, msg string) {
	s.render(w, r, status, "error", &Page{Title: http.StatusText(status), Data: map[string]any{"Status": status, "Message": msg}})
}

// templateFuncs are the helpers available in templates.
func templateFuncs(loc *time.Location) template.FuncMap {
	asTime := func(v any) (time.Time, bool) {
		switch t := v.(type) {
		case time.Time:
			return t, !t.IsZero()
		case *time.Time:
			if t != nil {
				return *t, !t.IsZero()
			}
		}
		return time.Time{}, false
	}
	return template.FuncMap{
		"date": func(v any) string {
			if t, ok := asTime(v); ok {
				return t.In(loc).Format("02 Jan 2006")
			}
			return "—"
		},
		"datetime": func(v any) string {
			if t, ok := asTime(v); ok {
				return t.In(loc).Format("02 Jan 2006, 15:04")
			}
			return "—"
		},
		"iso": func(v any) string {
			if t, ok := asTime(v); ok {
				return t.UTC().Format(time.RFC3339)
			}
			return ""
		},
		"rel": func(v any) string {
			t, ok := asTime(v)
			if !ok {
				return ""
			}
			return relative(time.Until(t))
		},
		"daysUntil": func(v any) int {
			t, ok := asTime(v)
			if !ok {
				return 0
			}
			return int(math.Ceil(time.Until(t).Hours() / 24))
		},
		"str": func(p *string) string {
			if p == nil {
				return ""
			}
			return *p
		},
		"reason": func(p *domain.EndReason) string {
			if p == nil {
				return "—"
			}
			return p.Label()
		},
		"durLabel": func(d domain.Duration) string { return d.Label() },
		"human":    human,
		"pct": func(a, b int) int {
			if b <= 0 {
				return 0
			}
			return int(math.Round(float64(a) * 100 / float64(b)))
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"name":     func(email string) string { return auth.Identity{Email: email}.DisplayName() },
		"initials": func(email string) string { return auth.Identity{Email: email}.Initials() },
		"shortID": func(id string) string {
			if i := strings.LastIndex(id, "/"); i >= 0 {
				return id[i+1:]
			}
			return id
		},
		"lower": strings.ToLower,
		// deref returns the value behind a pointer ("" for nil).
		"deref": func(v any) any {
			rv := reflect.ValueOf(v)
			if rv.Kind() == reflect.Pointer {
				if rv.IsNil() {
					return ""
				}
				return rv.Elem().Interface()
			}
			return v
		},
		// dict builds a map from key/value pairs (arguments of partials).
		"dict": func(kv ...any) map[string]any {
			m := make(map[string]any, len(kv)/2)
			for i := 0; i+1 < len(kv); i += 2 {
				m[fmt.Sprint(kv[i])] = kv[i+1]
			}
			return m
		},
		"stepIcon": stepIcon,
		"icon":     icon,
	}
}

// relative formats a duration as "in 3 days" / "2 hours ago".
func relative(d time.Duration) string {
	past := d < 0
	if past {
		d = -d
	}
	var s string
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		s = plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		s = plural(int(d.Hours()), "hour")
	default:
		s = plural(int(d.Hours()/24), "day")
	}
	if past {
		return s + " ago"
	}
	return "in " + s
}

// human formats a duration as "7 minutes", "3 hours".
func human(d time.Duration) string {
	switch {
	case d >= 24*time.Hour:
		return plural(int(d.Hours()/24), "day")
	case d >= time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Minutes()), "minute")
	}
}

func plural(n int, w string) string {
	if n == 1 {
		return "1 " + w
	}
	return fmt.Sprintf("%d %ss", n, w)
}

// stepIcon returns the icon of a step state.
func stepIcon(state domain.StepState, index int) template.HTML {
	switch state {
	case domain.StepOK:
		return icon("check")
	case domain.StepWarn:
		return icon("clock")
	case domain.StepKO:
		return icon("x")
	case domain.StepSkipped:
		return icon("minus")
	}
	return template.HTML(fmt.Sprintf("%d", index+1))
}

// icon returns an inline SVG icon (stroke icons, 24x24 grid).
func icon(name string) template.HTML {
	paths := map[string]string{
		"check":   `<path d="M5 12.5l4.2 4.2L19 7"/>`,
		"x":       `<path d="M6 6l12 12M18 6L6 18"/>`,
		"minus":   `<path d="M6 12h12"/>`,
		"clock":   `<circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/>`,
		"alert":   `<path d="M12 3.5l9.5 16.5h-19z"/><path d="M12 10v4.5M12 17.5v.01"/>`,
		"info":    `<circle cx="12" cy="12" r="9"/><path d="M12 11v5.5M12 7.5v.01"/>`,
		"ok":      `<circle cx="12" cy="12" r="9"/><path d="M8 12.3l2.8 2.8L16.5 9.5"/>`,
		"queue":   `<path d="M4 6h16M4 12h10M4 18h6"/><circle cx="18.5" cy="16.5" r="2.5"/>`,
		"refresh": `<path d="M20 11a8 8 0 10-2.3 5.7"/><path d="M20 4.5V11h-6.5"/>`,
		"user":    `<circle cx="12" cy="8" r="4"/><path d="M4 20c1.5-4 4.5-6 8-6s6.5 2 8 6"/>`,
		"send":    `<path d="M4 12L20 4l-6 16-2.5-6.5z"/>`,
		"link":    `<path d="M10 14a4.5 4.5 0 006.4 0l3-3a4.5 4.5 0 00-6.4-6.4l-1 1"/><path d="M14 10a4.5 4.5 0 00-6.4 0l-3 3a4.5 4.5 0 006.4 6.4l1-1"/>`,
		"book":    `<path d="M5 4.5h9a3 3 0 013 3V20H8a3 3 0 01-3-3z"/><path d="M5 17a3 3 0 013-3h9"/>`,
		"shield":  `<path d="M12 3l7.5 3v6c0 4.5-3.2 7.8-7.5 9-4.3-1.2-7.5-4.5-7.5-9V6z"/>`,
		"box":     `<path d="M12 3l8 4.5v9L12 21l-8-4.5v-9z"/><path d="M4 7.5l8 4.5 8-4.5M12 12v9"/>`,
	}
	p, ok := paths[name]
	if !ok {
		return ""
	}
	return template.HTML(`<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + p + `</svg>`)
}
