-- Several users with roles instead of a single administrator.
--
-- admin:    everything, including users, targets, and settings.
-- operator: reads everything, browses table rows, runs read-only SQL, and
--           acknowledges incidents.
-- viewer:   reads monitoring data only.
--
-- The administrator from LOCKLENS_ADMIN_USER / LOCKLENS_ADMIN_PASSWORD is
-- managed by the environment (managed = true): it is always an enabled admin
-- and cannot be changed or deleted in the UI, so access can always be
-- recovered by restarting with a new password.
ALTER TABLE admin_users RENAME TO users;
ALTER TABLE users
    ADD COLUMN role text NOT NULL DEFAULT 'viewer' CHECK (role IN ('admin', 'operator', 'viewer')),
    ADD COLUMN disabled boolean NOT NULL DEFAULT false,
    ADD COLUMN managed boolean NOT NULL DEFAULT false,
    ADD COLUMN last_login_at timestamptz;
-- The existing row is the environment administrator.
UPDATE users SET role = 'admin', managed = true;
-- User names are unique regardless of case.
ALTER TABLE users DROP CONSTRAINT admin_users_username_key;
CREATE UNIQUE INDEX users_username_lower ON users (lower(username));
