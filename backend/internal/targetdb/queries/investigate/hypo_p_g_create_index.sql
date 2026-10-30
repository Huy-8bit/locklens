-- Creates a hypothetical index in this session only ($1: a CREATE INDEX built by LockLens).
SELECT indexname FROM hypopg_create_index($1)
