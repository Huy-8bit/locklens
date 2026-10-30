SELECT extversion, current_setting('timescaledb.license', true) FROM pg_extension WHERE extname = 'timescaledb'
