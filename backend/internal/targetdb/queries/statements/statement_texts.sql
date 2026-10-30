-- The normalised texts of some queryids ($1). Reading texts reads the whole
-- text file of the extension, so it runs only for statements not seen before.
-- {{function}}: the schema-qualified pg_stat_statements function.
SELECT userid, dbid, toplevel, queryid, query FROM {{function}}(true) WHERE queryid = ANY($1)
