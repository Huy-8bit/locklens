-- Preserve elapsed collection time for duration-weighted rate downsampling.
-- Historical rows remain NULL: their observation intervals cannot be recovered.
ALTER TABLE metric_points ADD COLUMN interval_seconds double precision
    CHECK (interval_seconds > 0 AND interval_seconds < 'Infinity'::double precision);
