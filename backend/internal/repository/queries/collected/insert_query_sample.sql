INSERT INTO query_samples (target_id, identity_id, ts, interval_seconds, calls, total_exec_ms, rows,
    shared_blks_hit, shared_blks_read, temp_blks_written, wal_bytes)
SELECT $1, i, $2, $3, c, t, r, h, rd, tw, w
FROM unnest($4::bigint[], $5::bigint[], $6::float8[], $7::bigint[], $8::bigint[], $9::bigint[], $10::bigint[], $11::float8[])
     AS x(i, c, t, r, h, rd, tw, w)
ON CONFLICT DO NOTHING
