package store

import (
	"time"

	"github.com/francoisgn/gcp-gca-lic-mgmt/internal/domain"
)

// Subscription mirrors a row of the subscriptions table.
type Subscription struct {
	ID             string     `db:"id" json:"id"`
	DisplayName    string     `db:"display_name" json:"displayName"`
	Offer          string     `db:"offer" json:"offer"`
	State          string     `db:"state" json:"state"`
	IsCurrent      bool       `db:"is_current" json:"isCurrent"`
	SeatsTotal     int        `db:"seats_total" json:"seatsTotal"`
	SeatsAvailable int        `db:"seats_available" json:"seatsAvailable"`
	StartTime      *time.Time `db:"start_time" json:"startTime"`
	EndTime        *time.Time `db:"end_time" json:"endTime"`
	AutoRenew      bool       `db:"auto_renew" json:"autoRenew"`
	FirstSeenAt    time.Time  `db:"first_seen_at" json:"firstSeenAt"`
	LastSeenAt     time.Time  `db:"last_seen_at" json:"lastSeenAt"`
	InactiveSince  *time.Time `db:"inactive_since" json:"inactiveSince"`
	ReplacedBy     *string    `db:"replaced_by" json:"replacedBy"`
}

// ShortID returns the order ID part of the resource name.
func (s Subscription) ShortID() string {
	for i := len(s.ID) - 1; i >= 0; i-- {
		if s.ID[i] == '/' {
			return s.ID[i+1:]
		}
	}
	return s.ID
}

// Request mirrors a row of the license_requests table.
type Request struct {
	ID          int64                `db:"id" json:"id"`
	UserEmail   string               `db:"user_email" json:"userEmail"`
	Status      domain.RequestStatus `db:"status" json:"status"`
	Origin      string               `db:"origin" json:"origin"`
	CreatedAt   time.Time            `db:"created_at" json:"createdAt"`
	UpdatedAt   time.Time            `db:"updated_at" json:"updatedAt"`
	TrainingAck bool                 `db:"training_ack" json:"trainingAck"`
	Duration    domain.Duration      `db:"duration" json:"duration"`
	Answers     map[string]string    `db:"answers" json:"answers"`

	DirOutcome       *domain.DirectoryOutcome `db:"dir_outcome" json:"dirOutcome"`
	DirCheckedAt     *time.Time               `db:"dir_checked_at" json:"dirCheckedAt"`
	DirAccountStatus *string                  `db:"dir_account_status" json:"dirAccountStatus"`
	DirSite          *string                  `db:"dir_site" json:"dirSite"`
	DirBusinessUnit  *string                  `db:"dir_business_unit" json:"dirBusinessUnit"`
	DirGPSCode       *string                  `db:"dir_gps_code" json:"dirGpsCode"`
	DirReason        *string                  `db:"dir_reason" json:"dirReason"`

	EscalatedAt *time.Time `db:"escalated_at" json:"escalatedAt"`
	EscDivision *string    `db:"esc_division" json:"escDivision"`
	EscEntity   *string    `db:"esc_entity" json:"escEntity"`
	EscProject  *string    `db:"esc_project" json:"escProject"`
	EscComment  *string    `db:"esc_comment" json:"escComment"`

	Decision       *domain.Decision `db:"decision" json:"decision"`
	DecidedAt      *time.Time       `db:"decided_at" json:"decidedAt"`
	DecidedBy      *string          `db:"decided_by" json:"decidedBy"`
	DecisionReason *string          `db:"decision_reason" json:"decisionReason"`

	ReservedAt    *time.Time `db:"reserved_at" json:"reservedAt"` // seat reserved, license not recorded yet
	CredentialsAt *time.Time `db:"credentials_at" json:"credentialsAt"`
	LicenseAt     *time.Time `db:"license_at" json:"licenseAt"`
	EmailAt       *time.Time `db:"email_at" json:"emailAt"`
	QueuedAt      *time.Time `db:"queued_at" json:"queuedAt"`
	BufferedAt    *time.Time `db:"buffered_at" json:"bufferedAt"`
	CompletedAt   *time.Time `db:"completed_at" json:"completedAt"`
	FailureReason *string    `db:"failure_reason" json:"failureReason"`
	AssignmentID  *int64     `db:"assignment_id" json:"assignmentId"`
}

// WasEscalated reports whether the request went through admin validation.
func (r *Request) WasEscalated() bool { return r.EscalatedAt != nil }

// Assignment mirrors a row of the license_assignments table.
type Assignment struct {
	ID             int64                   `db:"id" json:"id"`
	UserEmail      string                  `db:"user_email" json:"userEmail"`
	SubscriptionID string                  `db:"subscription_id" json:"subscriptionId"`
	RequestID      *int64                  `db:"request_id" json:"requestId"`
	Source         domain.AssignmentSource `db:"source" json:"source"`
	AssignedBy     *string                 `db:"assigned_by" json:"assignedBy"`
	Duration       domain.Duration         `db:"duration" json:"duration"`
	AssignedAt     time.Time               `db:"assigned_at" json:"assignedAt"`
	ExpiresAt      time.Time               `db:"expires_at" json:"expiresAt"`
	LastUsageAt    *time.Time              `db:"last_usage_at" json:"lastUsageAt"`
	Site           *string                 `db:"site" json:"site"`
	BusinessUnit   *string                 `db:"business_unit" json:"businessUnit"`
	GPSCode        *string                 `db:"gps_code" json:"gpsCode"`
	MigratedAt     *time.Time              `db:"migrated_at" json:"migratedAt"`
	EndedAt        *time.Time              `db:"ended_at" json:"endedAt"`
	EndReason      *domain.EndReason       `db:"end_reason" json:"endReason"`
	EndedBy        *string                 `db:"ended_by" json:"endedBy"`
}

// QueueEntry mirrors a row of the queue_entries table.
type QueueEntry struct {
	ID         int64              `db:"id" json:"id"`
	RequestID  int64              `db:"request_id" json:"requestId"`
	UserEmail  string             `db:"user_email" json:"userEmail"`
	Priority   int                `db:"priority" json:"priority"`
	EnqueuedAt time.Time          `db:"enqueued_at" json:"enqueuedAt"`
	Status     domain.QueueStatus `db:"status" json:"status"`
	LeftAt     *time.Time         `db:"left_at" json:"leftAt"`
	Position   int                `db:"position" json:"position"` // computed by list queries
}

// BufferEntry mirrors a row of the buffer_entries table.
type BufferEntry struct {
	ID            int64               `db:"id" json:"id"`
	RequestID     int64               `db:"request_id" json:"requestId"`
	UserEmail     string              `db:"user_email" json:"userEmail"`
	Stage         domain.BufferStage  `db:"stage" json:"stage"`
	CreatedAt     time.Time           `db:"created_at" json:"createdAt"`
	Attempts      int                 `db:"attempts" json:"attempts"`
	LastAttemptAt *time.Time          `db:"last_attempt_at" json:"lastAttemptAt"`
	LastError     *string             `db:"last_error" json:"lastError"`
	Status        domain.BufferStatus `db:"status" json:"status"`
	ResolvedAt    *time.Time          `db:"resolved_at" json:"resolvedAt"`
	Position      int                 `db:"position" json:"position"`
}

// Deviation mirrors a row of the deviations table.
type Deviation struct {
	ID             int64                       `db:"id" json:"id"`
	UserEmail      string                      `db:"user_email" json:"userEmail"`
	SubscriptionID string                      `db:"subscription_id" json:"subscriptionId"`
	Kind           domain.DeviationKind        `db:"kind" json:"kind"`
	DetectedAt     time.Time                   `db:"detected_at" json:"detectedAt"`
	LastSeenAt     time.Time                   `db:"last_seen_at" json:"lastSeenAt"`
	ResolvedAt     *time.Time                  `db:"resolved_at" json:"resolvedAt"`
	Resolution     *domain.DeviationResolution `db:"resolution" json:"resolution"`
	ResolvedBy     *string                     `db:"resolved_by" json:"resolvedBy"`
}

// APICall mirrors a row of the api_calls table.
type APICall struct {
	TS         time.Time `db:"ts"`
	Target     string    `db:"target"`
	Operation  string    `db:"operation"`
	Context    string    `db:"context"`
	DurationMS int       `db:"duration_ms"`
	Outcome    string    `db:"outcome"`
	Detail     *string   `db:"detail"`
}

// Feedback mirrors a row of the feedback table.
type Feedback struct {
	ID           int64     `db:"id" json:"id"`
	CreatedAt    time.Time `db:"created_at" json:"createdAt"`
	UserEmail    string    `db:"user_email" json:"userEmail"`
	Category     string    `db:"category" json:"category"`
	Message      string    `db:"message" json:"message"`
	TrackerKey   *string   `db:"tracker_key" json:"trackerKey"`
	TrackerURL   *string   `db:"tracker_url" json:"trackerUrl"`
	TrackerError *string   `db:"tracker_error" json:"trackerError"`
}

// AuditEntry mirrors a row of the audit_log table.
type AuditEntry struct {
	ID      int64          `db:"id" json:"id"`
	TS      time.Time      `db:"ts" json:"ts"`
	Actor   string         `db:"actor" json:"actor"`
	Action  string         `db:"action" json:"action"`
	Target  string         `db:"target" json:"target"`
	Details map[string]any `db:"details" json:"details"`
}

// JobRun mirrors a row of the job_runs table.
type JobRun struct {
	ID         int64      `db:"id" json:"id"`
	Job        string     `db:"job" json:"job"`
	Trigger    string     `db:"trigger" json:"trigger"`
	Actor      *string    `db:"actor" json:"actor"`
	StartedAt  time.Time  `db:"started_at" json:"startedAt"`
	FinishedAt *time.Time `db:"finished_at" json:"finishedAt"`
	Status     string     `db:"status" json:"status"`
	Summary    *string    `db:"summary" json:"summary"`
	Error      *string    `db:"error" json:"error"`
}
