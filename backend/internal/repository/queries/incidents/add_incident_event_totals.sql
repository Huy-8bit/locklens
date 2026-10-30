UPDATE incidents SET event_count = event_count + $2, size_bytes = size_bytes + $3 WHERE id = $1
