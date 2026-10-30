DELETE FROM workspace_audit WHERE id < (SELECT coalesce(max(id),0)-10000 FROM workspace_audit)
