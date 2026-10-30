-- Columns other than pid and status are NULL without pg_read_all_stats.
SELECT status, sender_host, slot_name, last_msg_receipt_time FROM pg_catalog.pg_stat_wal_receiver
