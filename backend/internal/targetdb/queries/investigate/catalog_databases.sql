-- At most 200 rows are shown (201 are read to tell that there are more).
SELECT datname AS database, datname=current_database() AS accessible_in_workspace FROM pg_database WHERE datallowconn ORDER BY datname LIMIT 201
