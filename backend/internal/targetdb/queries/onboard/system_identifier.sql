-- Identifies the cluster (superusers only): roles LockLens created are
-- remembered per cluster.
SELECT system_identifier::text FROM pg_control_system()
