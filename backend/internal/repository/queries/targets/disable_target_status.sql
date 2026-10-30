UPDATE target_status SET state = 'disabled', updated_at = now() WHERE target_id = $1
