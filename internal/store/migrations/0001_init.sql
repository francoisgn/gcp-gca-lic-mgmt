-- =============================================================================
-- 0001 — initial schema of the Gemini Code Assist license portal.
-- Conventions:
--   * every e-mail is stored lower-cased (normalised by the application);
--   * every timestamp is TIMESTAMPTZ (UTC on the wire);
--   * statuses are TEXT constrained by CHECKs mirroring internal/domain.
-- =============================================================================

-- Subscriptions (= Google Cloud orders holding a Gemini Code Assist license pool).
CREATE TABLE subscriptions (
    id              TEXT PRIMARY KEY,               -- billingAccounts/{ba}/orders/{order}
    display_name    TEXT NOT NULL DEFAULT '',
    offer           TEXT NOT NULL DEFAULT '',
    state           TEXT NOT NULL CHECK (state IN ('ACTIVE', 'INACTIVE')),
    is_current      BOOLEAN NOT NULL DEFAULT FALSE, -- the pool used for new assignments
    seats_total     INT NOT NULL DEFAULT 0,
    seats_available INT NOT NULL DEFAULT 0,
    start_time      TIMESTAMPTZ,
    end_time        TIMESTAMPTZ,
    auto_renew      BOOLEAN NOT NULL DEFAULT FALSE,
    first_seen_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    inactive_since  TIMESTAMPTZ,
    replaced_by     TEXT REFERENCES subscriptions (id)
);
-- At most one current subscription.
CREATE UNIQUE INDEX subscriptions_one_current ON subscriptions (is_current) WHERE is_current;

-- License requests (one row per form submission).
CREATE TABLE license_requests (
    id                 BIGSERIAL PRIMARY KEY,
    user_email         TEXT NOT NULL,
    status             TEXT NOT NULL CHECK (status IN ('DRAFT', 'DIRECTORY_KO', 'PENDING_ADMIN', 'REJECTED',
                                                      'QUEUED', 'BUFFERED', 'ASSIGNED', 'FAILED', 'ABANDONED')),
    origin             TEXT NOT NULL DEFAULT 'FORM' CHECK (origin IN ('FORM', 'ADMIN', 'MIGRATION')),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT now(),
    training_ack       BOOLEAN NOT NULL DEFAULT FALSE,
    duration           TEXT NOT NULL,
    answers            JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- enterprise directory validation
    dir_outcome        TEXT CHECK (dir_outcome IN ('OK', 'FUNCTIONAL_KO', 'TECHNICAL_KO')),
    dir_checked_at     TIMESTAMPTZ,
    dir_account_status TEXT,
    dir_site           TEXT,
    dir_business_unit  TEXT,
    dir_gps_code       TEXT,
    dir_reason         TEXT,
    -- escalation to an administrator
    escalated_at       TIMESTAMPTZ,
    esc_division       TEXT,
    esc_entity         TEXT,
    esc_project        TEXT,
    esc_comment        TEXT,
    -- administrator decision
    decision           TEXT CHECK (decision IN ('APPROVED', 'REJECTED')),
    decided_at         TIMESTAMPTZ,
    decided_by         TEXT,
    decision_reason    TEXT,
    -- fulfilment
    credentials_at     TIMESTAMPTZ,
    license_at         TIMESTAMPTZ,
    email_at           TIMESTAMPTZ,
    queued_at          TIMESTAMPTZ,
    buffered_at        TIMESTAMPTZ,
    completed_at       TIMESTAMPTZ,
    failure_reason     TEXT,
    assignment_id      BIGINT
);
CREATE INDEX license_requests_user ON license_requests (user_email, created_at DESC);
CREATE INDEX license_requests_status ON license_requests (status);
-- A user has at most one open request.
CREATE UNIQUE INDEX license_requests_one_open ON license_requests (user_email)
    WHERE status IN ('DRAFT', 'DIRECTORY_KO', 'PENDING_ADMIN', 'QUEUED', 'BUFFERED');

-- License assignments (portal view; Google remains the source of truth).
CREATE TABLE license_assignments (
    id              BIGSERIAL PRIMARY KEY,
    user_email      TEXT NOT NULL,
    subscription_id TEXT NOT NULL REFERENCES subscriptions (id),
    request_id      BIGINT REFERENCES license_requests (id),
    source          TEXT NOT NULL CHECK (source IN ('AUTO', 'ADMIN', 'RECONCILED')),
    assigned_by     TEXT,
    duration        TEXT NOT NULL,
    assigned_at     TIMESTAMPTZ NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    last_usage_at   TIMESTAMPTZ,
    site            TEXT,             -- directory attributes, denormalised for inventory filters
    business_unit   TEXT,
    gps_code        TEXT,
    migrated_at     TIMESTAMPTZ,      -- last subscription rollover
    ended_at        TIMESTAMPTZ,
    end_reason      TEXT CHECK (end_reason IN ('UNUSED', 'EXPIRED', 'ADMIN_REMOVAL', 'EXTERNAL_REMOVAL',
                                               'SUBSCRIPTION_ENDED', 'USER_RELEASE')),
    ended_by        TEXT
);
CREATE UNIQUE INDEX license_assignments_one_active ON license_assignments (user_email) WHERE ended_at IS NULL;
CREATE INDEX license_assignments_assigned_at ON license_assignments (assigned_at);
CREATE INDEX license_assignments_ended_at ON license_assignments (ended_at);
CREATE INDEX license_assignments_subscription ON license_assignments (subscription_id) WHERE ended_at IS NULL;

ALTER TABLE license_requests
    ADD CONSTRAINT license_requests_assignment_fk FOREIGN KEY (assignment_id) REFERENCES license_assignments (id);

-- Waiting list when the pool is exhausted.
CREATE TABLE queue_entries (
    id          BIGSERIAL PRIMARY KEY,
    request_id  BIGINT NOT NULL REFERENCES license_requests (id),
    user_email  TEXT NOT NULL,
    priority    INT NOT NULL DEFAULT 0,
    enqueued_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    status      TEXT NOT NULL DEFAULT 'WAITING' CHECK (status IN ('WAITING', 'SERVED', 'BUFFERED', 'LEFT', 'CANCELLED')),
    left_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX queue_entries_one_waiting ON queue_entries (user_email) WHERE status = 'WAITING';
CREATE INDEX queue_entries_order ON queue_entries (priority DESC, enqueued_at, id) WHERE status = 'WAITING';

-- Buffer ("tampon"): requests whose external call timed out, retried by a job.
CREATE TABLE buffer_entries (
    id              BIGSERIAL PRIMARY KEY,
    request_id      BIGINT NOT NULL REFERENCES license_requests (id),
    user_email      TEXT NOT NULL,
    stage           TEXT NOT NULL CHECK (stage IN ('DIRECTORY', 'CREDENTIALS', 'LICENSE')),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    attempts        INT NOT NULL DEFAULT 0,
    last_attempt_at TIMESTAMPTZ,
    last_error      TEXT,
    status          TEXT NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'DONE', 'ABANDONED')),
    resolved_at     TIMESTAMPTZ
);
CREATE UNIQUE INDEX buffer_entries_one_pending ON buffer_entries (user_email) WHERE status = 'PENDING';

-- Deviations between the portal database and Google.
CREATE TABLE deviations (
    id              BIGSERIAL PRIMARY KEY,
    user_email      TEXT NOT NULL,
    subscription_id TEXT NOT NULL,
    kind            TEXT NOT NULL CHECK (kind IN ('GOOGLE_ONLY', 'DB_ONLY')),
    detected_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    resolved_at     TIMESTAMPTZ,
    resolution      TEXT CHECK (resolution IN ('REMOVED_FROM_GOOGLE', 'BACKPORTED', 'DB_CLOSED', 'REASSIGNED', 'SELF_HEALED')),
    resolved_by     TEXT
);
CREATE UNIQUE INDEX deviations_one_open ON deviations (user_email, subscription_id, kind) WHERE resolved_at IS NULL;

-- Outbound API calls (technical KPIs, retention handled by the housekeeping job).
CREATE TABLE api_calls (
    id          BIGSERIAL PRIMARY KEY,
    ts          TIMESTAMPTZ NOT NULL DEFAULT now(),
    target      TEXT NOT NULL,   -- DIRECTORY | GOOGLE_LICENSE | GOOGLE_ORDERS | GOOGLE_CREDENTIALS | TRACKER | MAIL
    operation   TEXT NOT NULL,   -- lookup | enumerate | getPool | assign | unassign | grant | revoke ...
    context     TEXT NOT NULL,   -- UX (user waiting) | JOB (scheduled) | ADMIN
    duration_ms INT NOT NULL,
    outcome     TEXT NOT NULL,   -- OK | TIMEOUT | HTTP_4XX | HTTP_5XX | NETWORK | NOT_FOUND | ERROR
    detail      TEXT
);
CREATE INDEX api_calls_ts ON api_calls (ts);

-- Daily counters that cannot be rebuilt from history.
CREATE TABLE daily_stats (
    day                   DATE PRIMARY KEY,
    page_views            INT NOT NULL DEFAULT 0,
    seats_total           INT,
    seats_assigned_google INT
);

-- User feedback forwarded to the issue tracker.
CREATE TABLE feedback (
    id            BIGSERIAL PRIMARY KEY,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    user_email    TEXT NOT NULL,
    category      TEXT NOT NULL CHECK (category IN ('IMPROVEMENT', 'BUG', 'QUESTION')),
    message       TEXT NOT NULL,
    tracker_key   TEXT,
    tracker_url   TEXT,
    tracker_error TEXT
);
CREATE INDEX feedback_user ON feedback (user_email, created_at DESC);

-- Administrator actions.
CREATE TABLE audit_log (
    id      BIGSERIAL PRIMARY KEY,
    ts      TIMESTAMPTZ NOT NULL DEFAULT now(),
    actor   TEXT NOT NULL,
    action  TEXT NOT NULL,
    target  TEXT NOT NULL DEFAULT '',
    details JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX audit_log_ts ON audit_log (ts DESC);

-- Scheduled job executions.
CREATE TABLE job_runs (
    id          BIGSERIAL PRIMARY KEY,
    job         TEXT NOT NULL,
    trigger     TEXT NOT NULL,  -- SCHEDULER | MANUAL | INPROCESS
    actor       TEXT,
    started_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    status      TEXT NOT NULL DEFAULT 'RUNNING' CHECK (status IN ('RUNNING', 'OK', 'ERROR', 'SKIPPED')),
    summary     TEXT,
    error       TEXT
);
CREATE INDEX job_runs_job ON job_runs (job, started_at DESC);
