-- {{table}} and {{segment_by}} come from the fixed list of hypertables.
ALTER TABLE {{table}} SET (timescaledb.compress, timescaledb.compress_segmentby = '{{segment_by}}', timescaledb.compress_orderby = 'ts')
