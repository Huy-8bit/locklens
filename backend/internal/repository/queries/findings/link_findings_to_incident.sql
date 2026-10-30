UPDATE findings SET incident_id = $1 WHERE id = ANY($2) AND incident_id IS NULL
