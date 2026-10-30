SELECT count(*) FROM drop_chunks($1::regclass, older_than => $2::timestamptz)
