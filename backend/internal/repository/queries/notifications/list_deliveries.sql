SELECT d.id, d.channel_id, c.name, d.incident_id, d.event, d.severity, d.status, d.attempts, d.error, d.at
FROM notification_deliveries d JOIN notification_channels c ON c.id = d.channel_id
WHERE $1::bigint IS NULL OR d.incident_id = $1
ORDER BY d.at DESC, d.id DESC LIMIT $2
