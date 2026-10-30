SELECT target_id, severity, count(*) FROM findings WHERE status = 'active' GROUP BY target_id, severity
