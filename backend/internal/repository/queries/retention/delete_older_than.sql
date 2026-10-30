-- {{table}}: a history table (see HistoryTables). At most 5,000 rows per run.
DELETE FROM {{table}} WHERE ctid IN (SELECT ctid FROM {{table}} WHERE ts < $1 LIMIT 5000)
