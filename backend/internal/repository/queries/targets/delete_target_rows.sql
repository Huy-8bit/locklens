-- {{table}}: a table with a target_id column (see PurgeTables). At most
-- 5,000 rows per run; the caller repeats it until fewer are deleted.
DELETE FROM {{table}} WHERE ctid IN (SELECT ctid FROM {{table}} WHERE target_id = $1 LIMIT 5000)
