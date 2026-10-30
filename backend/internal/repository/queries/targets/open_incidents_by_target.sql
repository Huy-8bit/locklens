SELECT target_id, count(*) FROM incidents WHERE state = 'open' AND target_id IS NOT NULL GROUP BY target_id
