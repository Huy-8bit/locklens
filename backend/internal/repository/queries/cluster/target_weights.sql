-- The measured cost (collector seconds per minute, averaged) and the manual
-- override of every target that has one.
SELECT target_id, busy_per_min, weight_override FROM target_weights
