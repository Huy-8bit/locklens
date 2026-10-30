-- Size functions take an ACCESS SHARE lock per relation: give up quickly
-- when a relation is locked exclusively (the caller falls back to estimates).
SET LOCAL lock_timeout = '250ms'
