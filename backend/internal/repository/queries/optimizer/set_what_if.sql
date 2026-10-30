UPDATE recommendations SET whatif = $3, confidence = $4 WHERE target_id = $1 AND id = $2
