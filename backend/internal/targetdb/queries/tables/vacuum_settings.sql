SELECT name, setting FROM pg_catalog.pg_settings
WHERE name IN ('autovacuum', 'autovacuum_freeze_max_age', 'autovacuum_multixact_freeze_max_age',
               'autovacuum_vacuum_scale_factor', 'autovacuum_vacuum_threshold',
               'autovacuum_analyze_scale_factor', 'autovacuum_naptime', 'autovacuum_max_workers',
               'vacuum_freeze_min_age', 'vacuum_freeze_table_age', 'track_counts')
