-- =============================================================================
-- 0003 — GPS code allowlist, monthly directory re-check, notification journal.
-- =============================================================================

-- GPS codes authorized for automatic approval. Managed by administrators in
-- the UI (Admin → GPS codes); every change is audited.
CREATE TABLE allowed_gps_codes (
    code        TEXT PRIMARY KEY CHECK (code = lower(code)),
    description TEXT NOT NULL DEFAULT '',
    added_by    TEXT NOT NULL,
    added_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Monthly re-check of active licenses against the enterprise directory.
-- Baseline = site / business_unit / gps_code recorded at assignment.
ALTER TABLE license_assignments
    ADD COLUMN dir_checked_at            TIMESTAMPTZ,  -- last successful check
    ADD COLUMN dir_check_result          TEXT CHECK (dir_check_result IN ('OK', 'CHANGED', 'INELIGIBLE', 'ERROR')),
    ADD COLUMN dir_check_error           TEXT,
    ADD COLUMN dir_current_status        TEXT,
    ADD COLUMN dir_current_site          TEXT,
    ADD COLUMN dir_current_business_unit TEXT,
    ADD COLUMN dir_current_gps_code      TEXT,
    ADD COLUMN drift_fields              TEXT[] NOT NULL DEFAULT '{}',
    ADD COLUMN drift_detected_at         TIMESTAMPTZ,
    ADD COLUMN ineligible_reason         TEXT;
CREATE INDEX license_assignments_dir_check ON license_assignments (dir_check_result) WHERE ended_at IS NULL;

-- Journal of every e-mail notification (sent, failed or disabled by policy).
CREATE TABLE notifications (
    id         BIGSERIAL PRIMARY KEY,
    ts         TIMESTAMPTZ NOT NULL DEFAULT now(),
    event      TEXT NOT NULL,
    recipient  TEXT NOT NULL,
    subject    TEXT NOT NULL,
    body_text  TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL CHECK (status IN ('SENT', 'FAILED', 'DISABLED')),
    error      TEXT,
    request_id BIGINT
);
CREATE INDEX notifications_ts ON notifications (ts DESC);
CREATE INDEX notifications_recipient ON notifications (recipient, ts DESC);
