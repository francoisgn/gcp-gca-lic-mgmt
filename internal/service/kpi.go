package service

import (
	"context"
	"errors"
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/store"
	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/telemetry"
)

// ObsSummary is the header of the observability page.
type ObsSummary struct {
	Subscription  *store.Subscription  `json:"subscription"`
	Subscriptions []store.Subscription `json:"subscriptions"`
	Counter       PoolCounter          `json:"counter"`
	QueueDepth    int                  `json:"queueDepth"`
	BufferDepth   int                  `json:"bufferDepth"`
	OpenDevs      int                  `json:"openDeviations"`
	PendingAdmin  int                  `json:"pendingAdmin"`
	Jobs          []store.JobRun       `json:"jobs"`
	GoogleMode    string               `json:"googleMode"`
	Mix           store.LicenseMix     `json:"mix"`
	ExternalPct   float64              `json:"externalPct"` // external active licenses / active licenses
}

// Summary returns the observability header.
func (s *Service) Summary(ctx context.Context) (*ObsSummary, error) {
	o := &ObsSummary{Counter: s.Counter(ctx), PendingAdmin: s.PendingCount(ctx), GoogleMode: s.Cfg.GoogleMode}
	var err error
	if o.Subscription, err = s.DB.CurrentSubscription(ctx); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	if o.Subscriptions, err = s.DB.ListSubscriptions(ctx); err != nil {
		return nil, err
	}
	if o.QueueDepth, err = s.DB.QueueDepth(ctx); err != nil {
		return nil, err
	}
	if o.BufferDepth, err = s.DB.BufferDepth(ctx); err != nil {
		return nil, err
	}
	devs, err := s.DB.OpenDeviations(ctx)
	if err != nil {
		return nil, err
	}
	o.OpenDevs = len(devs)
	if o.Jobs, err = s.DB.LastJobRuns(ctx); err != nil {
		return nil, err
	}
	if o.Mix, err = s.DB.LicenseMixStats(ctx, s.Policy.Accounts.ExternalSuffix); err != nil {
		return nil, err
	}
	if o.Mix.Active > 0 {
		o.ExternalPct = float64(o.Mix.External) * 100 / float64(o.Mix.Active)
	}
	return o, nil
}

// FlowsKPI feeds the waterfall charts.
type FlowsKPI struct {
	Points []store.FlowPoint `json:"points"`
}

// Flows returns the license flows of the last N days.
func (s *Service) Flows(ctx context.Context, days int) (*FlowsKPI, error) {
	pts, err := s.DB.LicenseFlows(ctx, s.Cfg.TimeZone.String(), clampDays(days, 90))
	if err != nil {
		return nil, err
	}
	// Seat counters before the first refresh: fall back to the current pool.
	cur, _ := s.DB.CurrentSubscription(ctx)
	for i := range pts {
		if pts[i].SeatsTotal == nil && cur != nil {
			pts[i].SeatsTotal = ptr(cur.SeatsTotal)
		}
	}
	return &FlowsKPI{Points: pts}, nil
}

// Depths returns the queue / buffer / deviation depth of the last N days.
func (s *Service) Depths(ctx context.Context, days int) ([]store.DepthPoint, error) {
	return s.DB.Depths(ctx, s.Cfg.TimeZone.String(), clampDays(days, 90))
}

// TechKPI groups the technical KPIs (10 days retention).
type TechKPI struct {
	Days      []string             `json:"days"`
	Directory []store.OutcomeCount `json:"directory"` // ED outcomes per day (stacked bars)
	Latencies []store.LatencyPoint `json:"latencies"` // p50 / p95 per target / operation
	Timeouts  []store.OutcomeCount `json:"timeouts"`  // TIMEOUT per context (UX vs JOB)
	Accesses  []store.AccessPoint  `json:"accesses"`  // page views vs Google checks
	Retention int                  `json:"retentionDays"`
}

// Tech returns the technical KPIs.
func (s *Service) Tech(ctx context.Context) (*TechKPI, error) {
	tz := s.Cfg.TimeZone.String()
	days := int(s.Cfg.MetricsRetention / (24 * time.Hour))
	k := &TechKPI{Retention: days}
	now := s.Now().In(s.Cfg.TimeZone)
	for i := days - 1; i >= 0; i-- {
		k.Days = append(k.Days, now.AddDate(0, 0, -i).Format("2006-01-02"))
	}
	var err error
	if k.Directory, err = s.DB.CallOutcomes(ctx, tz, days, telemetry.TargetDirectory); err != nil {
		return nil, err
	}
	if k.Latencies, err = s.DB.Latencies(ctx, tz, days); err != nil {
		return nil, err
	}
	all, err := s.DB.CallOutcomes(ctx, tz, days, "")
	if err != nil {
		return nil, err
	}
	for _, c := range all {
		if c.Outcome == telemetry.OutcomeTimeout {
			k.Timeouts = append(k.Timeouts, c)
		}
	}
	if k.Accesses, err = s.DB.Accesses(ctx, tz, days); err != nil {
		return nil, err
	}
	return k, nil
}

// Deviations returns the open deviations.
func (s *Service) Deviations(ctx context.Context) ([]store.Deviation, error) {
	return s.DB.OpenDeviations(ctx)
}

func clampDays(d, max int) int {
	if d <= 0 {
		return 30
	}
	if d > max {
		return max
	}
	return d
}
