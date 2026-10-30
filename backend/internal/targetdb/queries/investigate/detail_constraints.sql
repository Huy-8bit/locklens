-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT conname AS name,contype::text AS kind,pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid=$1 ORDER BY conname LIMIT 201
