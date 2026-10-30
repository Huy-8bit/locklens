-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT tgname AS name, pg_get_triggerdef(oid) AS definition, CASE tgenabled WHEN 'D' THEN false ELSE true END AS enabled FROM pg_trigger WHERE tgrelid=$1 AND NOT tgisinternal ORDER BY tgname LIMIT 201
