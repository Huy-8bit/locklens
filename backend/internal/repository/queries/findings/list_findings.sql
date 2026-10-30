SELECT id, target_id, incident_id, rule_id, rule_version, finding_key, severity, title, status, first_seen, last_seen,
       resolved_at, observed, evidence, explanation, possible_explanations, next_checks, limitations, confidence, metrics
FROM findings
WHERE ($1 = 0 OR target_id = $1) AND ($2 = 0 OR incident_id = $2)
  AND ($3 = '' OR ($3 = 'current' AND status <> 'resolved') OR ($3 = 'resolved' AND status = 'resolved'))
ORDER BY CASE severity WHEN 'critical' THEN 0 WHEN 'warning' THEN 1 ELSE 2 END, last_seen DESC
LIMIT $4
