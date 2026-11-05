SELECT target_id, rule_id, severity, count(*)
FROM incidents
WHERE state = 'open' AND target_id = ANY ($1::bigint[])
GROUP BY target_id, rule_id, severity
