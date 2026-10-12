package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/auth"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/jobs"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/service"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
)

// adminPage renders approvals, waiting list, buffer, manual actions, audit.
func (s *Server) adminPage(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	o, err := s.svc.Overview(r.Context())
	if err != nil {
		return err
	}
	s.render(w, r, http.StatusOK, "admin", &Page{
		Title: "Administration", Nav: "admin",
		Data: map[string]any{"O": o, "Policy": s.svc.Policy, "Questions": s.svc.Policy.Questions},
		JSON: map[string]any{"durations": s.svc.Policy.Durations, "defaultDuration": s.svc.Policy.DefaultDuration},
	})
	return nil
}

// observabilityPage renders the KPI page; charts are loaded by obs.js.
func (s *Server) observabilityPage(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	sum, err := s.svc.Summary(r.Context())
	if err != nil {
		return err
	}
	var jobList []map[string]string
	for _, j := range s.runner.Jobs() {
		jobList = append(jobList, map[string]string{"name": j.Name, "description": j.Description, "cron": j.Cron})
	}
	s.render(w, r, http.StatusOK, "observability", &Page{
		Title: "Observability", Nav: "observability",
		Data: map[string]any{"S": sum, "Jobs": jobList, "Super": id.Role.IsSuperAdmin()},
		JSON: map[string]any{
			"super": id.Role.IsSuperAdmin(), "fake": s.cfg.GoogleMode == "fake",
			"durations": s.svc.Policy.Durations, "defaultDuration": s.svc.Policy.DefaultDuration,
			"unusedDays": s.svc.Policy.UnusedDays,
		},
	})
	return nil
}

// --- Admin API ----------------------------------------------------------------------

func (s *Server) apiApprove(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	reqID, err := pathID(r)
	if err != nil {
		return err
	}
	res, err := s.svc.Approve(r.Context(), id.Email, reqID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiReject(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	reqID, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Reason string `json:"reason"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.svc.Reject(r.Context(), id.Email, reqID, in.Reason); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) apiAssign(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Email    string `json:"email"`
		Duration string `json:"duration"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.svc.AdminAssign(r.Context(), id.Email, in.Email, in.Duration)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiTransfer(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		From     string `json:"from"`
		To       string `json:"to"`
		Duration string `json:"duration"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.svc.Transfer(r.Context(), id.Email, in.From, in.To, in.Duration)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiUnassign(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Emails []string `json:"emails"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.svc.Unassign(r.Context(), id.Email, in.Emails)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiUnassignByFilter(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Filter   store.InventoryFilter `json:"filter"`
		Expected int                   `json:"expected"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	res, err := s.svc.UnassignByFilter(r.Context(), id.Email, in.Filter, in.Expected)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiInventory(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	var f store.InventoryFilter
	if err := readJSON(r, &f); err != nil {
		return err
	}
	v, err := s.svc.Inventory(r.Context(), f)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiCancelQueue(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	entryID, err := pathID(r)
	if err != nil {
		return err
	}
	if err := s.svc.CancelQueueEntry(r.Context(), id.Email, entryID); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

func (s *Server) apiRetryBuffer(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	entryID, err := pathID(r)
	if err != nil {
		return err
	}
	outcome, err := s.svc.RetryBufferEntry(r.Context(), id.Email, entryID)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
	return nil
}

// --- Observability API ----------------------------------------------------------------

func (s *Server) apiObsSummary(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	v, err := s.svc.Summary(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiObsFlows(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	v, err := s.svc.Flows(r.Context(), days)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiObsDepths(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	v, err := s.svc.Depths(r.Context(), days)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiObsTech(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	v, err := s.svc.Tech(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiObsDeviations(w http.ResponseWriter, r *http.Request, _ auth.Identity) error {
	v, err := s.svc.Deviations(r.Context())
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, v)
	return nil
}

func (s *Server) apiResolveDeviation(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	devID, err := pathID(r)
	if err != nil {
		return err
	}
	var in struct {
		Action string `json:"action"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.svc.ResolveDeviation(r.Context(), id.Email, devID, in.Action); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}

// --- Super admin API ----------------------------------------------------------------

func (s *Server) apiForceRefresh(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	return s.runJob(w, r.Context(), "subscription-refresh", id.Email)
}

func (s *Server) apiRunJob(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	return s.runJob(w, r.Context(), r.PathValue("name"), id.Email)
}

func (s *Server) runJob(w http.ResponseWriter, ctx context.Context, name, actor string) error {
	res, err := s.runner.Run(ctx, name, jobs.TriggerManual, actor)
	if err != nil {
		return &service.UserError{Status: http.StatusBadRequest, Msg: err.Error()}
	}
	writeJSON(w, http.StatusOK, res)
	return nil
}

func (s *Server) apiSimRollover(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Seats int `json:"seats"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	summary, err := s.svc.SimRollover(r.Context(), id.Email, in.Seats)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
	return nil
}

func (s *Server) apiSimSeats(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Seats int `json:"seats"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	summary, err := s.svc.SimSetSeats(r.Context(), id.Email, in.Seats)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"summary": summary})
	return nil
}

func (s *Server) apiSimRemove(w http.ResponseWriter, r *http.Request, id auth.Identity) error {
	var in struct {
		Email string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil {
		return err
	}
	if err := s.svc.SimRemoveInGoogle(r.Context(), id.Email, in.Email); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	return nil
}
