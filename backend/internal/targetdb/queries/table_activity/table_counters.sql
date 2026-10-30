-- The cumulative counters of pg_stat_user_tables only: no size functions and
-- no relation locks, so it is cheap enough to run every minute.
SELECT t.relid, t.schemaname, t.relname, coalesce(t.seq_scan, 0), coalesce(t.seq_tup_read, 0),
       coalesce(t.idx_scan, 0), coalesce(t.idx_tup_fetch, 0),
       t.n_tup_ins + t.n_tup_upd + t.n_tup_del, t.n_dead_tup, t.n_live_tup
FROM pg_catalog.pg_stat_user_tables AS t
