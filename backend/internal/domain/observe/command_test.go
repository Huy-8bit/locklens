package observe

import "testing"

func TestClassifyCommand(t *testing.T) {
	cases := map[string]string{
		"SELECT * FROM items WHERE id = $1":                                             CommandSelect,
		"  select 1":                                                                    CommandSelect,
		"-- comment\nSELECT 1":                                                          CommandSelect,
		"/* leading */ INSERT INTO items (name) VALUES ($1)":                            CommandInsert,
		"UPDATE items SET qty = qty + $1 WHERE id = $2":                                 CommandUpdate,
		"DELETE FROM items WHERE id = $1":                                               CommandDelete,
		"MERGE INTO t USING s ON t.id = s.id WHEN MATCHED THEN UPDATE SET x=1":          CommandMerge,
		"WITH a AS (SELECT 1) SELECT * FROM a":                                          CommandSelect,
		"WITH moved AS (DELETE FROM a RETURNING *) INSERT INTO b SELECT * FROM moved":   CommandInsert,
		"WITH x AS (SELECT 1) UPDATE t SET v = 1":                                       CommandUpdate,
		"with recursive r(n) as (select 1 union all select n+1 from r) select n from r": CommandSelect,
		"WITH a AS (SELECT 'insert' AS s) SELECT * FROM a":                              CommandSelect,
		"COPY items FROM STDIN":                                                         CommandCopy,
		"CREATE INDEX CONCURRENTLY i ON t (c)":                                          CommandDDL,
		"ALTER TABLE t ADD COLUMN c int":                                                CommandDDL,
		"TRUNCATE t":                                                                    CommandDDL,
		"BEGIN":                                                                         CommandTransaction,
		"COMMIT":                                                                        CommandTransaction,
		"ROLLBACK TO SAVEPOINT s":                                                       CommandTransaction,
		"PREPARE TRANSACTION 'gid'":                                                     CommandTransaction,
		"PREPARE q AS SELECT $1":                                                        CommandSelect,
		"VACUUM (ANALYZE) t":                                                            CommandMaintenance,
		"SET application_name = $1":                                                     CommandUtility,
		"SHOW work_mem":                                                                 CommandUtility,
		"EXPLAIN SELECT 1":                                                              CommandUtility,
		"DO $$ BEGIN DELETE FROM t; END $$":                                             CommandUtility,
		"":                                                                              CommandOther,
		"   ":                                                                           CommandOther,
		"<insufficient privilege>":                                                      CommandOther,
		"\"weird\" stuff":                                                               CommandOther,
	}
	for sql, want := range cases {
		if got := ClassifyCommand(sql); got != want {
			t.Errorf("ClassifyCommand(%q) = %s, want %s", sql, got, want)
		}
	}
}

func TestClassifyCommandUnterminated(t *testing.T) {
	// Malformed input must not panic or loop.
	for _, s := range []string{"/* never closed", "SELECT 'open", "WITH a AS (SELECT $tag$ x", "$", "$1", "((((("} {
		_ = ClassifyCommand(s)
	}
}
