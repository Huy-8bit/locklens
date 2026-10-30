INSERT INTO findings (target_id, rule_id, rule_version, finding_key, severity, title, status, first_seen, last_seen,
    observed, evidence, explanation, possible_explanations, next_checks, limitations, confidence, metrics)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (target_id, rule_id, finding_key) WHERE status <> 'resolved' DO UPDATE SET
    rule_version = EXCLUDED.rule_version, severity = EXCLUDED.severity, title = EXCLUDED.title,
    status = EXCLUDED.status, last_seen = EXCLUDED.last_seen, observed = EXCLUDED.observed,
    evidence = EXCLUDED.evidence, explanation = EXCLUDED.explanation,
    possible_explanations = EXCLUDED.possible_explanations, next_checks = EXCLUDED.next_checks,
    limitations = EXCLUDED.limitations, confidence = EXCLUDED.confidence, metrics = EXCLUDED.metrics
RETURNING id
