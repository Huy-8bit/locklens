INSERT INTO notification_deliveries (channel_id, incident_id, event, severity, status, attempts, error, at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
