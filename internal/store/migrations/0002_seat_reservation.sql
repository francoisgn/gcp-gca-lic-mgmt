-- =============================================================================
-- 0002 — seat reservation.
-- The pool advisory lock is now held only to check availability and reserve
-- a seat (milliseconds). Slow Google calls (credentials, assignment) run
-- outside the lock; the reservation keeps the seat for the request meanwhile.
-- A reservation is released when the license is recorded (license_at set),
-- when the request is queued, or when it ends (failed / abandoned).
-- =============================================================================
ALTER TABLE license_requests ADD COLUMN reserved_at TIMESTAMPTZ;
CREATE INDEX license_requests_reserved ON license_requests (reserved_at)
    WHERE reserved_at IS NOT NULL AND license_at IS NULL;
