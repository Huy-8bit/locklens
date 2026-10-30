SELECT compression_enabled FROM timescaledb_information.hypertables
WHERE hypertable_schema = current_schema() AND hypertable_name = $1
