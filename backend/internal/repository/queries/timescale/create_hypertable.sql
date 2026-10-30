SELECT create_hypertable($1::regclass, by_range('ts', $2::interval), migrate_data => true)
