-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT 'parent' AS relationship,inhparent::regclass::text AS relation FROM pg_inherits WHERE inhrelid=$1 UNION ALL SELECT 'child',inhrelid::regclass::text FROM pg_inherits WHERE inhparent=$1 LIMIT 201
