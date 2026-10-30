SELECT catalog FROM catalog_state WHERE target_id = $1 ORDER BY collected_at DESC LIMIT 1
