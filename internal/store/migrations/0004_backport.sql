-- =============================================================================
-- 0004 — Back-port: licenses found in Google but missing from the database.
-- =============================================================================

-- When the portal adopted the license (deviation GOOGLE_ONLY resolved by an
-- administrator, after an enterprise directory check). NULL for licenses
-- obtained through the portal. Source of the inventory "back-ported" filter
-- and of the user's progress panel.
ALTER TABLE license_assignments ADD COLUMN backported_at TIMESTAMPTZ;
UPDATE license_assignments SET backported_at = assigned_at WHERE source = 'RECONCILED';
