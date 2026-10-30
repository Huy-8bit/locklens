DELETE FROM catalog_state WHERE target_id = $1 AND NOT datname = ANY ($2::text[])
