package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/policy"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
)

// homeJSON is the data consumed by home.js (progressive form).
type homeJSON struct {
	State         string                  `json:"state"`
	Email         string                  `json:"email"`
	Name          string                  `json:"name"`
	Durations     []policy.DurationOption `json:"durations"`
	Questions     []policy.Question       `json:"questions"`
	Prefill       *store.Request          `json:"prefill"`
	PoolExhausted bool                    `json:"poolExhausted"`
	NextPosition  int                     `json:"nextPosition"`
	TrainingURL   string                  `json:"trainingUrl"`
	Admins        []string                `json:"admins"`
	UXTimeoutMS   int64                   `json:"uxTimeoutMs"`
	Steps         any                     `json:"steps"`
}

// homePage renders the landing page: subscription status and, when needed,
// the progressive request form.
func (s *Server) homePage(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	page, _ := strconv.Atoi(r.URL.Query().Get("rp"))
	v, err := s.svc.Home(r.Context(), id, page)
	if err != nil {
		return err
	}
	s.svc.RecordPageView(r.Context())
	pol := s.svc.Policy
	s.render(w, r, http.StatusOK, "home", &Page{
		Title: "My license", Nav: "home",
		Data: map[string]any{"View": v, "Policy": pol},
		JSON: homeJSON{
			State: v.State, Email: id.Email, Name: id.DisplayName(),
			Durations: pol.Durations, Questions: pol.Questions, Prefill: v.Request,
			PoolExhausted: v.PoolExhausted, NextPosition: v.NextPosition,
			TrainingURL: pol.Links.Training, Admins: v.Admins,
			UXTimeoutMS: s.cfg.UXTimeout.Milliseconds(), Steps: v.Steps,
		},
	})
	return nil
}

// feedbackPage renders the feedback form and the user's previous feedback.
func (s *Server) feedbackPage(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	list, err := s.svc.MyFeedback(r.Context(), id)
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "feedback", &Page{Title: "Feedback", Nav: "feedback", Data: map[string]any{"Items": list}})
	return nil
}

// --- JSON API ----------------------------------------------------------------------

func (s *Server) apiDirectory(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in service.FormInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.svc.CheckDirectory(r.Context(), id, in)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiSubmit(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	reqID, err := pathID(r)
	if err != nil {
		return err
	}
	res, err := s.svc.Submit(r.Context(), id, reqID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiEscalate(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	reqID, err := pathID(r)
	if err != nil {
		return err
	}
	var in service.EscalationInput
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if _, err := s.svc.Escalate(r.Context(), id, reqID, in); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]any{"admins": s.roles.Admins()})
	return nil
}

func (s *Server) apiExit(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	reqID, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.svc.ExitRequest(r.Context(), id, reqID); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) apiLeaveQueue(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	if err := s.svc.LeaveQueue(r.Context(), id); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) apiFeedback(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Category string `json:"category"`
		Message  string `json:"message"`
		Page     string `json:"page"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	f, err := s.svc.SubmitFeedback(r.Context(), id, in.Category, in.Message, s.cfg.BaseURL+in.Page)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, f)
	return nil
}

func (s *Server) apiCounter(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	writeJSON(w, http.StatusOK, s.svc.Counter(r.Context()))
	return nil
}

// --- Development identity switcher ---------------------------------------------

func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	email := r.FormValue("email")
	http.SetCookie(w, &http.Cookie{Name: auth.DevCookie, Value: email, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Expires: time.Now().Add(30 * 24 * time.Hour)})
	back := r.FormValue("back")
	if back == "" || back[0] != '/' || (len(back) > 1 && back[1] == '/') {
		back = "/"
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// pathID parses the {id} path parameter.
func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return 0, &service.UserError{Status: http.StatusBadRequest, Msg: "Invalid identifier."}
	}
	return id, nil
}
