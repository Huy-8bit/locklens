-- A hypertable's rows live in its chunks; compressed chunks report their
-- size before compression too.
SELECT c.relname,
       CASE WHEN h.hypertable_name IS NULL THEN pg_total_relation_size(c.oid) ELSE hypertable_size(c.oid) END,
       CASE WHEN h.hypertable_name IS NULL THEN greatest(c.reltuples, 0)::bigint ELSE approximate_row_count(c.oid) END,
       cs.before_compression_total_bytes, cs.after_compression_total_bytes
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN timescaledb_information.hypertables h ON h.hypertable_schema = n.nspname AND h.hypertable_name = c.relname
LEFT JOIN LATERAL (SELECT * FROM hypertable_compression_stats(c.oid) WHERE h.compression_enabled AND h.hypertable_name IS NOT NULL) cs ON true
WHERE n.nspname = current_schema() AND c.relkind = 'r'
ORDER BY 2 DESC
