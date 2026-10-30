INSERT INTO recommendations (target_id, datname, kind, rec_key, severity, title, summary, details, ddl, benefit, confidence, first_seen, last_seen)
VALUES ($1, $12, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $11)
ON CONFLICT (target_id, datname, kind, rec_key) DO UPDATE SET
    last_seen = EXCLUDED.last_seen,
    status = CASE WHEN recommendations.status = 'resolved' THEN 'open' ELSE recommendations.status END,
    resolved_at = CASE WHEN recommendations.status = 'resolved' THEN NULL ELSE recommendations.resolved_at END,
    severity = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.severity ELSE recommendations.severity END,
    title = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.title ELSE recommendations.title END,
    summary = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.summary ELSE recommendations.summary END,
    details = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.details ELSE recommendations.details END,
    ddl = CASE WHEN recommendations.status IN ('open', 'accepted', 'resolved') THEN EXCLUDED.ddl ELSE recommendations.ddl END,
    benefit = EXCLUDED.benefit,
    confidence = CASE WHEN recommendations.whatif IS NULL THEN EXCLUDED.confidence ELSE recommendations.confidence END
