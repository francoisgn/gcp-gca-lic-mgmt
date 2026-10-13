// Package domain holds the vocabulary shared by every layer of the portal:
// request / assignment / queue / buffer statuses, end reasons, step states
// and license durations. It has no dependency on infrastructure code.
package domain

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// License requests
// ---------------------------------------------------------------------------

// RequestStatus is the lifecycle state of a license request.
type RequestStatus string

const (
	// ReqDraft: the form was submitted up to the directory step but the
	// directory has not validated it yet (transient).
	ReqDraft RequestStatus = "DRAFT"
	// ReqDirectoryKO: the directory answered with a functional KO. The user
	// must choose between exiting and escalating to an administrator.
	ReqDirectoryKO RequestStatus = "DIRECTORY_KO"
	// ReqPendingAdmin: escalated, waiting for an administrator decision.
	ReqPendingAdmin RequestStatus = "PENDING_ADMIN"
	// ReqRejected: an administrator rejected the escalation (terminal).
	ReqRejected RequestStatus = "REJECTED"
	// ReqQueued: validated but the license pool is exhausted.
	ReqQueued RequestStatus = "QUEUED"
	// ReqBuffered: a call to an external service timed out; the request sits
	// in the buffer table and is retried by a scheduled job.
	ReqBuffered RequestStatus = "BUFFERED"
	// ReqAssigned: credentials and license granted (terminal, success).
	ReqAssigned RequestStatus = "ASSIGNED"
	// ReqFailed: non-retryable technical failure (terminal).
	ReqFailed RequestStatus = "FAILED"
	// ReqAbandoned: user exited after a directory KO or left the queue (terminal).
	ReqAbandoned RequestStatus = "ABANDONED"
)

// OpenRequestStatuses are the statuses for which a user can have at most one
// request at a time (enforced by a partial unique index in the database).
var OpenRequestStatuses = []RequestStatus{ReqDraft, ReqDirectoryKO, ReqPendingAdmin, ReqQueued, ReqBuffered}

// IsOpen reports whether the status is a non-terminal one.
func (s RequestStatus) IsOpen() bool {
	for _, o := range OpenRequestStatuses {
		if s == o {
			return true
		}
	}
	return false
}

// DirectoryOutcome is the result of the enterprise directory validation.
type DirectoryOutcome string

const (
	DirOK           DirectoryOutcome = "OK"
	DirFunctionalKO DirectoryOutcome = "FUNCTIONAL_KO" // account KO / GPS code not eligible
	DirTechnicalKO  DirectoryOutcome = "TECHNICAL_KO"  // timeout, 5xx, network
)

// Decision is an administrator decision on an escalated request.
type Decision string

const (
	DecisionApproved Decision = "APPROVED"
	DecisionRejected Decision = "REJECTED"
)

// ---------------------------------------------------------------------------
// License assignments
// ---------------------------------------------------------------------------

// AssignmentSource tells how a license was granted. It feeds the waterfall KPI.
type AssignmentSource string

const (
	SourceAuto       AssignmentSource = "AUTO"       // directory validated, no human involved
	SourceAdmin      AssignmentSource = "ADMIN"      // escalation approved, manual assignment or transfer
	SourceReconciled AssignmentSource = "RECONCILED" // back-ported from Google (deviation resolution)
)

// EndReason tells why an assignment ended. It is displayed to the user in the
// license history and feeds the waterfall KPI.
type EndReason string

const (
	EndUnused            EndReason = "UNUSED"             // no usage for N days
	EndExpired           EndReason = "EXPIRED"            // requested duration elapsed
	EndAdminRemoval      EndReason = "ADMIN_REMOVAL"      // removed by an administrator
	EndExternalRemoval   EndReason = "EXTERNAL_REMOVAL"   // removed outside the portal (deviation)
	EndSubscriptionEnded EndReason = "SUBSCRIPTION_ENDED" // subscription ended, no seat on the new one
	EndUserRelease       EndReason = "USER_RELEASE"       // reserved for a future "release my license" action
)

// Label returns a human readable (English) label for an end reason.
func (r EndReason) Label() string {
	switch r {
	case EndUnused:
		return "Unused"
	case EndExpired:
		return "Expired"
	case EndAdminRemoval:
		return "Admin removal"
	case EndExternalRemoval:
		return "Removed outside the portal"
	case EndSubscriptionEnded:
		return "Not kept at renewal"
	case EndUserRelease:
		return "Released by user"
	}
	return string(r)
}

// ---------------------------------------------------------------------------
// Queue and buffer
// ---------------------------------------------------------------------------

// QueueStatus is the state of a waiting-list entry.
type QueueStatus string

const (
	QueueWaiting   QueueStatus = "WAITING"
	QueueServed    QueueStatus = "SERVED"    // a license was assigned
	QueueBuffered  QueueStatus = "BUFFERED"  // moved to the buffer (timeout while serving)
	QueueLeft      QueueStatus = "LEFT"      // user left the queue
	QueueCancelled QueueStatus = "CANCELLED" // cancelled by an administrator or failure
)

// Queue priorities: higher is served first, FIFO within a priority.
const (
	PriorityNormal    = 0
	PriorityBuffered  = 5  // directory-buffered users who end up queued keep their head start
	PriorityMigration = 10 // users who lost their seat during a subscription rollover
)

// BufferStage is the pipeline stage that timed out and must be retried.
type BufferStage string

const (
	StageDirectory   BufferStage = "DIRECTORY"
	StageCredentials BufferStage = "CREDENTIALS"
	StageLicense     BufferStage = "LICENSE"
)

// BufferStatus is the state of a buffer entry.
type BufferStatus string

const (
	BufferPending   BufferStatus = "PENDING"
	BufferDone      BufferStatus = "DONE"
	BufferAbandoned BufferStatus = "ABANDONED"
)

// ---------------------------------------------------------------------------
// Deviations between the database and Google (source of truth)
// ---------------------------------------------------------------------------

// DeviationKind tells on which side a license exists.
type DeviationKind string

const (
	DeviationGoogleOnly DeviationKind = "GOOGLE_ONLY" // assigned in Google, unknown to the portal
	DeviationDBOnly     DeviationKind = "DB_ONLY"     // active in the portal, absent from Google
)

// DeviationResolution is how a deviation was closed.
type DeviationResolution string

const (
	ResolveRemoveFromGoogle DeviationResolution = "REMOVED_FROM_GOOGLE" // GOOGLE_ONLY -> unassign in Google
	ResolveBackport         DeviationResolution = "BACKPORTED"          // GOOGLE_ONLY -> create the DB record
	ResolveCloseDB          DeviationResolution = "DB_CLOSED"           // DB_ONLY -> close the DB record
	ResolveReassign         DeviationResolution = "REASSIGNED"          // DB_ONLY -> assign again in Google
	ResolveSelfHealed       DeviationResolution = "SELF_HEALED"         // difference disappeared by itself
)

// ---------------------------------------------------------------------------
// Stepper (left panel)
// ---------------------------------------------------------------------------

// StepID identifies a step of the request process displayed in the left panel.
type StepID string

const (
	StepIdentity    StepID = "identity"
	StepTraining    StepID = "training"
	StepDuration    StepID = "duration"
	StepDetails     StepID = "details"
	StepDirectory   StepID = "directory"
	StepAdmin       StepID = "admin"
	StepCredentials StepID = "credentials"
	StepLicense     StepID = "license"
	StepEmail       StepID = "email"
)

// StepState is the visual state of a step.
type StepState string

const (
	StepTodo    StepState = "todo"    // grey
	StepActive  StepState = "active"  // current step, highlighted
	StepOK      StepState = "ok"      // green
	StepWarn    StepState = "warn"    // orange (buffered / waiting)
	StepKO      StepState = "ko"      // red
	StepSkipped StepState = "skipped" // grey, not needed (admin validation)
)

// Step is one line of the left panel.
type Step struct {
	ID    StepID    `json:"id"`
	Label string    `json:"label"`
	State StepState `json:"state"`
	Hint  string    `json:"hint,omitempty"`
}

// StepDefinitions lists the steps in display order with their English labels.
var StepDefinitions = []Step{
	{ID: StepIdentity, Label: "Identity"},
	{ID: StepTraining, Label: "Gemini Code Assist training"},
	{ID: StepDuration, Label: "Duration"},
	{ID: StepDetails, Label: "Usage details"},
	{ID: StepDirectory, Label: "Directory validation"},
	{ID: StepAdmin, Label: "Admin validation"},
	{ID: StepCredentials, Label: "Cloud credentials"},
	{ID: StepLicense, Label: "License assignment"},
	{ID: StepEmail, Label: "Confirmation email"},
}

// NewSteps returns a fresh copy of the step list with every step in state todo
// (admin validation is skipped unless an escalation happens).
func NewSteps() []Step {
	out := make([]Step, len(StepDefinitions))
	copy(out, StepDefinitions)
	for i := range out {
		out[i].State = StepTodo
		if out[i].ID == StepAdmin {
			out[i].State = StepSkipped
		}
	}
	return out
}

// SetStep updates the state (and optional hint) of one step in place.
func SetStep(steps []Step, id StepID, state StepState, hint string) {
	for i := range steps {
		if steps[i].ID == id {
			steps[i].State = state
			steps[i].Hint = hint
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Durations
// ---------------------------------------------------------------------------

// Duration is a license duration code as selected in the form: 7d, 1M, 3M, 6M, 1Y.
type Duration string

// ParseDuration validates a duration code.
func ParseDuration(s string) (Duration, error) {
	d := Duration(strings.TrimSpace(s))
	if _, _, err := d.parts(); err != nil {
		return "", err
	}
	return d, nil
}

// parts splits "3M" into (3, 'M').
func (d Duration) parts() (int, byte, error) {
	s := string(d)
	if len(s) < 2 {
		return 0, 0, fmt.Errorf("invalid duration %q", s)
	}
	unit := s[len(s)-1]
	var n int
	if _, err := fmt.Sscanf(s[:len(s)-1], "%d", &n); err != nil || n <= 0 {
		return 0, 0, fmt.Errorf("invalid duration %q", s)
	}
	switch unit {
	case 'd', 'w', 'M', 'Y':
		return n, unit, nil
	}
	return 0, 0, fmt.Errorf("invalid duration unit in %q (expected d, w, M or Y)", s)
}

// AddTo returns t + duration using calendar arithmetic (months and years
// follow the calendar, days are 24h days).
func (d Duration) AddTo(t time.Time) time.Time {
	n, unit, err := d.parts()
	if err != nil {
		return t
	}
	switch unit {
	case 'd':
		return t.AddDate(0, 0, n)
	case 'w':
		return t.AddDate(0, 0, 7*n)
	case 'M':
		return t.AddDate(0, n, 0)
	case 'Y':
		return t.AddDate(n, 0, 0)
	}
	return t
}

// Label returns an English label: "7 days", "1 month", "1 year"...
func (d Duration) Label() string {
	n, unit, err := d.parts()
	if err != nil {
		return string(d)
	}
	word := map[byte]string{'d': "day", 'w': "week", 'M': "month", 'Y': "year"}[unit]
	if n > 1 {
		word += "s"
	}
	return fmt.Sprintf("%d %s", n, word)
}

// NormalizeEmail lower-cases and trims an e-mail address. Every e-mail stored
// in the database goes through this function.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
