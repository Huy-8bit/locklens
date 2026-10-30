UPDATE targets SET enabled = false, deleted_at = coalesce(deleted_at, now()) WHERE id = $1
