SELECT min(ts),max(ts) FROM metric_points WHERE target_id=$1
