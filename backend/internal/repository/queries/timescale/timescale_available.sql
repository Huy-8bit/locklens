SELECT EXISTS (SELECT 1 FROM pg_available_extensions WHERE name = 'timescaledb'),
       current_setting('shared_preload_libraries')
