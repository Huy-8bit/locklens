UPDATE notification_silences SET starts_at = least(starts_at, now() - interval '1 millisecond'), ends_at = now()
WHERE id = $1 AND ends_at > now()
