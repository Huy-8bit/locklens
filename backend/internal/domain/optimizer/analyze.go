// Package optimizer derives query optimization recommendations from the
// schema catalog and the statement workload: missing and redundant indexes,
// foreign keys without an index, invalid indexes, stale statistics,
// misestimated row counts, temporary-file spills, and statement shapes that
// often perform badly. Recommendations are deterministic, carry their
// evidence and a confidence, and come with the SQL for a person to run;
// LockLens never changes the database itself. Index recommendations can be
// checked with hypothetical indexes (HypoPG) when the target allows it, and
// every applied change is measured before and after.
package optimizer

import (
	"cmp"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/locklens/locklens/internal/domain/observe"
	"github.com/locklens/locklens/internal/lib/sqlparse"
	"github.com/locklens/locklens/internal/repository"
)

// Recommendation kinds.
const (
	KindMissingIndex   = "missing_index"
	KindFKIndex        = "fk_without_index"
	KindUnusedIndex    = "unused_index"
	KindDuplicateIndex = "duplicate_index"
	KindInvalidIndex   = "invalid_index"
	KindStaleStats     = "stale_statistics"
	KindMisestimate    = "misestimate"
	KindTempSpill      = "temp_spill"
	KindQueryShape     = "query_shape"
)

// Limits of the analysis.
const (
	// minTableRows: smaller tables are read cheaply without an index.
	minTableRows = 10000
	// minIndexBytes: smaller unused indexes are not worth a recommendation.
	minIndexBytes = 1 << 20
	// minStatsAge: idx_scan = 0 means little before statistics are this old.
	minStatsAge = 7 * 24 * time.Hour
	// maxIndexColumns bounds a proposed index.
	maxIndexColumns = 4
	// tempBlocksPerCall: temp writes per call above this (8 MiB) spill.
	tempBlocksPerCall = 1024
	// misestimateRatio: estimate and actual rows this far apart.
	misestimateRatio = 10
)

// Confidence grades how well the evidence supports a recommendation.
type Confidence struct {
	Level   string   `json:"level"`
	Reasons []string `json:"reasons"`
}

// StatementRef is a statement a recommendation is about.
type StatementRef struct {
	IdentityID int64   `json:"identity_id"`
	QueryID    string  `json:"queryid"`
	Share      float64 `json:"share_of_load"`
	Calls      int64   `json:"calls"`
	MeanMS     float64 `json:"mean_ms"`
	Text       string  `json:"text,omitempty"`
}

// Input is everything one optimizer pass looks at for one target.
type Input struct {
	Now     time.Time
	Catalog observe.Catalog
	// Statements are the busiest statements of the last day.
	Statements []repository.WorkloadStatement
	// Plans are the latest saved plans by identity.
	Plans map[int64]ParsedPlan
	// Parser says whether statement texts can be parsed.
	Parser bool
}

// ParsedPlan is what the optimizer needs of a saved plan.
type ParsedPlan struct {
	TopRows   float64
	Relations []string
	Analyzed  bool
}

// Analyze derives the recommendations of a target.
func Analyze(in Input) []repository.RecommendationInput {
	cat := newCatalogIndex(in.Catalog)
	var out []repository.RecommendationInput
	if in.Parser {
		parsed := parseStatements(in.Statements, in.Catalog.DatName)
		out = append(out, missingIndexes(parsed, cat)...)
		out = append(out, queryShapes(parsed)...)
		out = append(out, misestimates(parsed, in.Plans, cat)...)
	}
	// A standby has its primary's schema: structural recommendations come
	// from the primary (where they can be applied), not once per replica.
	// Its own workload still counts; changes are made on the primary.
	if !in.Catalog.InRecovery {
		out = append(out, fkWithoutIndex(cat)...)
		out = append(out, unusedIndexes(cat, in.Catalog.StatsReset, in.Now)...)
		out = append(out, duplicateIndexes(cat)...)
		out = append(out, invalidIndexes(cat)...)
		out = append(out, staleStatistics(cat, in.Now)...)
	}
	out = append(out, tempSpills(in.Statements, in.Catalog)...)
	if in.Catalog.InRecovery {
		for i := range out {
			if out[i].DDL != nil && out[i].Kind != KindTempSpill {
				out[i].Summary += " This instance is a standby: apply the change on its primary."
			}
		}
	}
	slices.SortStableFunc(out, func(a, b repository.RecommendationInput) int { return cmp.Compare(b.Benefit, a.Benefit) })
	return out
}

// catalogIndex looks up tables and indexes by name.
type catalogIndex struct {
	cat     observe.Catalog
	tables  map[string]observe.CatalogTable // schema.name
	byName  map[string][]string             // name -> schema.name
	indexes map[string][]observe.CatalogIndex
	columns map[string]observe.ColumnStats // schema.table.column
}

func newCatalogIndex(c observe.Catalog) catalogIndex {
	ci := catalogIndex{cat: c, tables: map[string]observe.CatalogTable{}, byName: map[string][]string{},
		indexes: map[string][]observe.CatalogIndex{}, columns: map[string]observe.ColumnStats{}}
	for _, t := range c.Tables {
		ci.tables[t.Qualified()] = t
		ci.byName[t.Name] = append(ci.byName[t.Name], t.Qualified())
	}
	for _, i := range c.Indexes {
		ci.indexes[i.TableQualified()] = append(ci.indexes[i.TableQualified()], i)
	}
	for _, col := range c.Columns {
		ci.columns[col.Schema+"."+col.Table+"."+col.Column] = col
	}
	return ci
}

// resolve finds the catalog table of a name from a statement: qualified,
// or unqualified and unique (public preferred).
func (c catalogIndex) resolve(name string) (observe.CatalogTable, bool) {
	if name == "" {
		return observe.CatalogTable{}, false
	}
	if t, ok := c.tables[name]; ok {
		return t, true
	}
	if strings.Contains(name, ".") {
		return observe.CatalogTable{}, false
	}
	cands := c.byName[name]
	if t, ok := c.tables["public."+name]; ok {
		return t, true
	}
	if len(cands) == 1 {
		return c.tables[cands[0]], true
	}
	return observe.CatalogTable{}, false
}

// distinct estimates a column's number of distinct values (0: unknown).
func (c catalogIndex) distinct(t observe.CatalogTable, column string) float64 {
	s, ok := c.columns[t.Qualified()+"."+column]
	if !ok {
		return 0
	}
	if s.NDistinct < 0 {
		return -s.NDistinct * math.Max(t.RelTuples, 1)
	}
	return s.NDistinct
}

type parsedStatement struct {
	repository.WorkloadStatement
	analysis sqlparse.Analysis
}

func parseStatements(stmts []repository.WorkloadStatement, datname string) []parsedStatement {
	var out []parsedStatement
	for _, s := range stmts {
		if s.Text == nil || s.DatName != datname || s.Calls == 0 {
			continue
		}
		a, err := sqlparse.Analyze(*s.Text)
		if err != nil {
			continue
		}
		out = append(out, parsedStatement{WorkloadStatement: s, analysis: a})
	}
	return out
}

func ref(s parsedStatement) StatementRef {
	text := ""
	if s.Text != nil {
		text = excerpt(*s.Text, 500)
	}
	return StatementRef{IdentityID: s.IdentityID, QueryID: fmt.Sprint(s.Key.QueryID), Share: s.Share, Calls: s.Calls,
		MeanMS: s.TotalMS / float64(max(s.Calls, 1)), Text: text}
}

func excerpt(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// candidate is a proposed index of one table.
type candidate struct {
	table      observe.CatalogTable
	columns    []string
	statements []StatementRef
	share      float64
	reasons    []string
}

// proposeIndex orders a statement's predicates on one table into index
// columns: equalities first (the most selective first when statistics are
// visible), then one range column, then the ORDER BY columns when the
// statement has a LIMIT and only equalities precede them.
func proposeIndex(c catalogIndex, t observe.CatalogTable, preds []sqlparse.Predicate, order []sqlparse.OrderColumn, limit bool) []string {
	var eq, rng []string
	for _, p := range preds {
		switch {
		case p.Wrapped != "" || p.InOr:
		case p.Equality() && p.Op != "IS NULL":
			if !slices.Contains(eq, p.Column) {
				eq = append(eq, p.Column)
			}
		case p.Range():
			if !slices.Contains(rng, p.Column) {
				rng = append(rng, p.Column)
			}
		}
	}
	slices.SortStableFunc(eq, func(a, b string) int { return cmp.Compare(c.distinct(t, b), c.distinct(t, a)) })
	cols := slices.Clone(eq)
	if len(rng) > 0 && !slices.Contains(cols, rng[0]) {
		cols = append(cols, rng[0])
	} else if len(rng) == 0 && limit {
		for _, o := range order {
			if tt, ok := c.resolve(o.Table); ok && tt.Qualified() == t.Qualified() && !slices.Contains(cols, o.Column) {
				cols = append(cols, o.Column)
			}
		}
	}
	if len(cols) > maxIndexColumns {
		cols = cols[:maxIndexColumns]
	}
	return cols
}

// covered reports whether an existing valid B-tree index already serves the
// leading column of a candidate (or its equality set): then a new index
// would add little and is not proposed.
func covered(c catalogIndex, t observe.CatalogTable, cols []string) (observe.CatalogIndex, bool) {
	for _, ix := range c.indexes[t.Qualified()] {
		if !ix.Valid || ix.Partial || ix.Method != "btree" || len(ix.Columns) == 0 || ix.Columns[0] == "" {
			continue
		}
		if ix.Columns[0] == cols[0] {
			return ix, true
		}
	}
	return observe.CatalogIndex{}, false
}

func missingIndexes(stmts []parsedStatement, c catalogIndex) []repository.RecommendationInput {
	byKey := map[string]*candidate{}
	var order []string
	for _, s := range stmts {
		byTable := map[string][]sqlparse.Predicate{}
		for _, p := range s.analysis.Predicates {
			if t, ok := c.resolve(p.Table); ok {
				byTable[t.Qualified()] = append(byTable[t.Qualified()], p)
			}
		}
		for q, preds := range byTable {
			t := c.tables[q]
			if t.RelTuples < minTableRows {
				continue
			}
			cols := proposeIndex(c, t, preds, s.analysis.OrderBy, s.analysis.HasLimit)
			if len(cols) == 0 {
				continue
			}
			if _, ok := covered(c, t, cols); ok {
				continue
			}
			key := q + "(" + strings.Join(cols, ",") + ")"
			cd := byKey[key]
			if cd == nil {
				cd = &candidate{table: t, columns: cols}
				byKey[key] = cd
				order = append(order, key)
			}
			cd.statements = append(cd.statements, ref(s))
			cd.share += s.Share
		}
	}
	var out []repository.RecommendationInput
	for _, key := range order {
		cd := byKey[key]
		t := cd.table
		reasons := []string{"derived from the statements' conditions with PostgreSQL's parser, not from an execution plan"}
		level := "low"
		if t.SeqScan > t.IdxScan && t.SeqScan > 0 {
			avg := float64(t.SeqTupRead) / float64(t.SeqScan)
			reasons = append(reasons, fmt.Sprintf("%s was read sequentially %d times (about %.0f rows each) and by index %d times since statistics were reset", t.Qualified(), t.SeqScan, avg, t.IdxScan))
			level = "medium"
		}
		if cd.share >= 0.05 {
			reasons = append(reasons, fmt.Sprintf("the statements use %.0f%% of all statement execution time", cd.share*100))
		}
		known := 0
		for _, col := range cd.columns {
			if c.distinct(t, col) > 0 {
				known++
			}
		}
		if known == 0 {
			reasons = append(reasons, "column statistics are not visible, so the column order follows the statements only")
		}
		reasons = append(reasons, "check it with a hypothetical index (HypoPG) or EXPLAIN on a copy before creating it")
		sev := "info"
		if cd.share >= 0.1 {
			sev = "warning"
		}
		ddl := fmt.Sprintf("CREATE INDEX CONCURRENTLY ON %s (%s);", quoteQualified(t.Schema, t.Name), quoteList(cd.columns))
		slices.SortFunc(cd.statements, func(a, b StatementRef) int { return cmp.Compare(b.Share, a.Share) })
		out = append(out, repository.RecommendationInput{
			Kind: KindMissingIndex, Key: key, Severity: sev,
			Title:   fmt.Sprintf("Index on %s (%s)", t.Qualified(), strings.Join(cd.columns, ", ")),
			Summary: fmt.Sprintf("%d statement(s) filter %s (about %s rows) on %s without an index starting with %s.", len(cd.statements), t.Qualified(), compact(t.RelTuples), strings.Join(cd.columns, ", "), cd.columns[0]),
			Details: map[string]any{"table": t.Qualified(), "columns": cd.columns, "statements": cd.statements, "rows": t.RelTuples,
				"seq_scan": t.SeqScan, "seq_tup_read": t.SeqTupRead, "idx_scan": t.IdxScan, "share_of_load": cd.share},
			DDL: &ddl, Benefit: benefit(cd.share*100, 5), Confidence: Confidence{Level: level, Reasons: reasons},
		})
	}
	return out
}

func queryShapes(stmts []parsedStatement) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, s := range stmts {
		for _, l := range s.analysis.Lints {
			if l.Code == sqlparse.LintMultiStatement {
				continue
			}
			weight := 20.0
			if l.Severity == "warning" {
				weight = 50
			}
			r := ref(s)
			out = append(out, repository.RecommendationInput{
				Kind: KindQueryShape, Key: fmt.Sprintf("%d:%s", s.IdentityID, l.Code), Severity: l.Severity,
				Title:   fmt.Sprintf("Statement %d: %s", s.Key.QueryID, lintTitle(l.Code)),
				Summary: l.Message,
				Details: map[string]any{"lint": l, "statement": r},
				Benefit: benefit(s.Share*weight, 1),
				Confidence: Confidence{Level: "medium", Reasons: []string{"found by PostgreSQL's parser in the normalised statement text",
					"whether it matters depends on the data and the plan; the statement's share of load ranks it"}},
			})
		}
	}
	return out
}

var lintTitles = map[string]string{
	sqlparse.LintSelectStar: "SELECT *", sqlparse.LintOffset: "OFFSET pagination", sqlparse.LintLeadingWildcard: "leading wildcard pattern",
	sqlparse.LintFunctionOnColumn: "function on an indexed column", sqlparse.LintNotInSubquery: "NOT IN (subquery)",
	sqlparse.LintOrAcrossColumns: "OR across columns", sqlparse.LintNoWhere: "write without WHERE", sqlparse.LintCartesian: "cartesian join",
}

func lintTitle(code string) string {
	if t, ok := lintTitles[code]; ok {
		return t
	}
	return code
}

func misestimates(stmts []parsedStatement, plans map[int64]ParsedPlan, c catalogIndex) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, s := range stmts {
		p, ok := plans[s.IdentityID]
		if !ok || s.analysis.Command != "select" || s.Calls < 10 {
			continue
		}
		actual := float64(s.Rows) / float64(s.Calls)
		est := p.TopRows
		ratio := math.Max(est, 1) / math.Max(actual, 1)
		if ratio < 1 {
			ratio = 1 / ratio
		}
		if ratio < misestimateRatio {
			continue
		}
		// Several equality conditions on one table: correlated columns are
		// a classic cause, which extended statistics address.
		eq := map[string][]string{}
		for _, pr := range s.analysis.Predicates {
			if t, ok := c.resolve(pr.Table); ok && pr.Op == "=" && pr.Wrapped == "" && !slices.Contains(eq[t.Qualified()], pr.Column) {
				eq[t.Qualified()] = append(eq[t.Qualified()], pr.Column)
			}
		}
		var ddl []string
		for _, rel := range p.Relations {
			if t, ok := c.resolve(rel); ok {
				ddl = append(ddl, fmt.Sprintf("ANALYZE %s;", quoteQualified(t.Schema, t.Name)))
			}
		}
		for q, cols := range eq {
			if len(cols) >= 2 {
				t := c.tables[q]
				ddl = append(ddl, fmt.Sprintf("CREATE STATISTICS ON %s FROM %s; -- then ANALYZE", quoteList(cols), quoteQualified(t.Schema, t.Name)))
			}
		}
		if len(ddl) == 0 {
			continue
		}
		sql := strings.Join(slices.Compact(ddl), "\n")
		dir := "more"
		if est < actual {
			dir = "fewer"
		}
		out = append(out, repository.RecommendationInput{
			Kind: KindMisestimate, Key: fmt.Sprint(s.IdentityID), Severity: "warning",
			Title:   fmt.Sprintf("Statement %d: the planner expects %.0fx %s rows than it gets", s.Key.QueryID, ratio, dir),
			Summary: fmt.Sprintf("The saved plan estimates %s rows; the statement returns %s rows per call on average. Wrong estimates lead to wrong join methods and memory sizing.", compact(est), compact(actual)),
			Details: map[string]any{"statement": ref(s), "estimated_rows": est, "actual_rows_per_call": actual, "ratio": ratio, "relations": p.Relations},
			DDL:     &sql, Benefit: benefit(s.Share*80, 5),
			Confidence: Confidence{Level: "medium", Reasons: []string{"estimated rows from the latest saved plan; actual rows per call from pg_stat_statements",
				"a generic plan estimates for typical parameters, so very skewed parameters also differ"}},
		})
	}
	return out
}

func fkWithoutIndex(c catalogIndex) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, fk := range c.cat.ForeignKeys {
		q := fk.Schema + "." + fk.Table
		t, ok := c.tables[q]
		if !ok || t.RelTuples < 1000 || len(fk.Columns) == 0 {
			continue
		}
		if fkIndexed(c.indexes[q], fk.Columns) {
			continue
		}
		parent := c.tables[fk.RefSchema+"."+fk.RefTable]
		sev, weight := "info", 15.0
		reasons := []string{fmt.Sprintf("%s has about %s rows and no valid index starting with %s", q, compact(t.RelTuples), strings.Join(fk.Columns, ", "))}
		if parent.Writes > 0 || fk.OnDelete == "c" {
			sev, weight = "warning", 40
			reasons = append(reasons, fmt.Sprintf("deleting or updating a key of %s.%s scans %s for referencing rows while holding locks, which can block other sessions", fk.RefSchema, fk.RefTable, q))
		}
		ddl := fmt.Sprintf("CREATE INDEX CONCURRENTLY ON %s (%s);", quoteQualified(fk.Schema, fk.Table), quoteList(fk.Columns))
		out = append(out, repository.RecommendationInput{
			Kind: KindFKIndex, Key: q + ":" + fk.Name, Severity: sev,
			Title:   fmt.Sprintf("Index the foreign key %s on %s (%s)", fk.Name, q, strings.Join(fk.Columns, ", ")),
			Summary: fmt.Sprintf("Every DELETE or key UPDATE on %s.%s must find the referencing rows in %s; without an index that is a full scan per row.", fk.RefSchema, fk.RefTable, q),
			Details: map[string]any{"table": q, "columns": fk.Columns, "constraint": fk.Name, "references": fk.RefSchema + "." + fk.RefTable,
				"on_delete": fk.OnDelete, "rows": t.RelTuples, "parent_writes": parent.Writes},
			DDL: &ddl, Benefit: benefit(weight*math.Min(1, math.Log10(math.Max(t.RelTuples, 10))/7), 1),
			Confidence: Confidence{Level: "high", Reasons: reasons},
		})
	}
	return out
}

// fkIndexed reports whether an index's leading key columns are the foreign
// key's columns (in any order).
func fkIndexed(indexes []observe.CatalogIndex, cols []string) bool {
	for _, ix := range indexes {
		if !ix.Valid || ix.Partial || len(ix.Columns) < len(cols) {
			continue
		}
		lead := slices.Clone(ix.Columns[:len(cols)])
		want := slices.Clone(cols)
		slices.Sort(lead)
		slices.Sort(want)
		if slices.Equal(lead, want) {
			return true
		}
	}
	return false
}

func unusedIndexes(c catalogIndex, reset *time.Time, now time.Time) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, ix := range c.cat.Indexes {
		if ix.Scans == nil || *ix.Scans != 0 || ix.Unique || ix.Primary || ix.Constraint || !ix.Valid || ix.SizeBytes < minIndexBytes {
			continue
		}
		level, reasons := "medium", []string{"idx_scan is 0: no query used the index since statistics were reset"}
		switch {
		case reset == nil:
			level = "low"
			reasons = append(reasons, "when statistics were last reset is unknown")
		case now.Sub(*reset) < minStatsAge:
			level = "low"
			reasons = append(reasons, fmt.Sprintf("statistics were reset only %s ago", now.Sub(*reset).Round(time.Hour)))
		default:
			reasons = append(reasons, fmt.Sprintf("statistics cover %.0f days", now.Sub(*reset).Hours()/24))
		}
		reasons = append(reasons, "scans on standbys are not counted here: check every replica before dropping", "a new index also shows 0 until it is used")
		sev := "info"
		if ix.SizeBytes >= 100<<20 {
			sev = "warning"
		}
		ddl := fmt.Sprintf("DROP INDEX CONCURRENTLY %s;", quoteQualified(ix.Schema, ix.Name))
		out = append(out, repository.RecommendationInput{
			Kind: KindUnusedIndex, Key: ix.Qualified(), Severity: sev,
			Title:   fmt.Sprintf("Unused index %s (%s)", ix.Qualified(), bytesLabel(ix.SizeBytes)),
			Summary: fmt.Sprintf("%s on %s has never been used since statistics were reset, but every write to the table maintains it.", ix.Qualified(), ix.TableQualified()),
			Details: map[string]any{"index": ix.Qualified(), "table": ix.TableQualified(), "definition": ix.Definition, "size_bytes": ix.SizeBytes, "stats_reset": reset},
			DDL:     &ddl, Benefit: benefit(5+10*math.Min(1, float64(ix.SizeBytes)/float64(1<<30)), 1),
			Confidence: Confidence{Level: level, Reasons: reasons},
		})
	}
	return out
}

func duplicateIndexes(c catalogIndex) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	keep := func(ix observe.CatalogIndex) bool { return ix.Unique || ix.Primary || ix.Constraint }
	for _, list := range c.indexes {
		for _, a := range list {
			if !a.Valid || a.Partial || a.Expressions || a.Method != "btree" || keep(a) {
				continue
			}
			ak := a.Columns[:min(a.KeyColumns, len(a.Columns))]
			for _, b := range list {
				if a.RelID == b.RelID || !b.Valid || b.Partial || b.Expressions || b.Method != "btree" {
					continue
				}
				bk := b.Columns[:min(b.KeyColumns, len(b.Columns))]
				same := slices.Equal(ak, bk)
				prefix := len(ak) < len(bk) && slices.Equal(ak, bk[:len(ak)])
				// Of two identical indexes keep the one that backs a
				// constraint, else the one with the smaller OID.
				if !prefix && !(same && (keep(b) || b.RelID < a.RelID)) {
					continue
				}
				why := fmt.Sprintf("%s (%s) is a leading prefix of %s (%s), which can serve the same lookups", a.Qualified(), strings.Join(ak, ", "), b.Qualified(), strings.Join(bk, ", "))
				if same {
					why = fmt.Sprintf("%s has the same columns (%s) as %s", a.Qualified(), strings.Join(ak, ", "), b.Qualified())
				}
				ddl := fmt.Sprintf("DROP INDEX CONCURRENTLY %s;", quoteQualified(a.Schema, a.Name))
				out = append(out, repository.RecommendationInput{
					Kind: KindDuplicateIndex, Key: a.Qualified(), Severity: "info",
					Title:   fmt.Sprintf("Redundant index %s", a.Qualified()),
					Summary: why + ". Every write maintains both.",
					Details: map[string]any{"index": a.Qualified(), "covered_by": b.Qualified(), "definition": a.Definition, "covering_definition": b.Definition,
						"size_bytes": a.SizeBytes, "identical": same},
					DDL: &ddl, Benefit: benefit(8+10*math.Min(1, float64(a.SizeBytes)/float64(1<<30)), 1),
					Confidence: Confidence{Level: "high", Reasons: []string{why, "a longer index is slightly larger to scan; keep the shorter one only if measurements show it matters"}},
				})
				break
			}
		}
	}
	return out
}

func invalidIndexes(c catalogIndex) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, ix := range c.cat.Indexes {
		if ix.Valid {
			continue
		}
		ddl := fmt.Sprintf("REINDEX INDEX CONCURRENTLY %s;  -- or DROP INDEX CONCURRENTLY and create it again", quoteQualified(ix.Schema, ix.Name))
		out = append(out, repository.RecommendationInput{
			Kind: KindInvalidIndex, Key: ix.Qualified(), Severity: "warning",
			Title:   fmt.Sprintf("Invalid index %s", ix.Qualified()),
			Summary: fmt.Sprintf("%s is invalid (usually a failed CREATE INDEX CONCURRENTLY): queries cannot use it, yet writes may still maintain it.", ix.Qualified()),
			Details: map[string]any{"index": ix.Qualified(), "table": ix.TableQualified(), "definition": ix.Definition, "size_bytes": ix.SizeBytes},
			DDL:     &ddl, Benefit: 30,
			Confidence: Confidence{Level: "high", Reasons: []string{"pg_index.indisvalid is false"}},
		})
	}
	return out
}

func staleStatistics(c catalogIndex, now time.Time) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	for _, t := range c.cat.Tables {
		rows := math.Max(float64(t.LiveTuples), t.RelTuples)
		if rows < minTableRows || float64(t.ModSinceAnalyze) < 0.2*rows {
			continue
		}
		last := t.LastAnalyze
		if t.LastAutoAnalyze != nil && (last == nil || t.LastAutoAnalyze.After(*last)) {
			last = t.LastAutoAnalyze
		}
		if last != nil && now.Sub(*last) < 24*time.Hour {
			continue
		}
		when := "never"
		if last != nil {
			when = fmt.Sprintf("%.0f days ago", now.Sub(*last).Hours()/24)
		}
		ddl := fmt.Sprintf("ANALYZE %s;", quoteQualified(t.Schema, t.Name))
		out = append(out, repository.RecommendationInput{
			Kind: KindStaleStats, Key: t.Qualified(), Severity: "info",
			Title:   fmt.Sprintf("Stale statistics on %s", t.Qualified()),
			Summary: fmt.Sprintf("%s rows of %s changed since it was last analysed (%s): the planner estimates from outdated statistics.", compact(float64(t.ModSinceAnalyze)), t.Qualified(), when),
			Details: map[string]any{"table": t.Qualified(), "rows": rows, "modified_since_analyze": t.ModSinceAnalyze, "last_analyze": last},
			DDL:     &ddl, Benefit: benefit(15+15*math.Min(1, float64(t.ModSinceAnalyze)/rows), 1),
			Confidence: Confidence{Level: "medium", Reasons: []string{"n_mod_since_analyze from pg_stat_user_tables",
				"autovacuum analyzes after autovacuum_analyze_scale_factor of the rows change; large tables may wait long"}},
		})
	}
	return out
}

func tempSpills(stmts []repository.WorkloadStatement, cat observe.Catalog) []repository.RecommendationInput {
	var out []repository.RecommendationInput
	var workKB float64
	_, _ = fmt.Sscan(cat.Settings["work_mem"], &workKB)
	for _, s := range stmts {
		if s.Calls < 10 {
			continue
		}
		perCall := float64(s.TempW) / float64(s.Calls)
		if perCall < tempBlocksPerCall {
			continue
		}
		spillMB := perCall * 8 / 1024
		// Spilled data is usually smaller on disk than the memory the sort or
		// hash needs: suggest twice the spill, rounded up to a power of two.
		suggestMB := math.Pow(2, math.Ceil(math.Log2(math.Max(spillMB*2, 4))))
		suggestMB = math.Min(suggestMB, 1024)
		if workKB > 0 && suggestMB*1024 <= workKB {
			continue
		}
		ddl := fmt.Sprintf("-- For the statement's transaction only:\nSET LOCAL work_mem = '%.0fMB';\n-- Or for the role (every sort and hash node of every session may use it):\nALTER ROLE %s SET work_mem = '%.0fMB';", suggestMB, quoteIdent(s.UserName), suggestMB)
		r := StatementRef{IdentityID: s.IdentityID, QueryID: fmt.Sprint(s.Key.QueryID), Share: s.Share, Calls: s.Calls, MeanMS: s.TotalMS / float64(s.Calls)}
		if s.Text != nil {
			r.Text = excerpt(*s.Text, 500)
		}
		current := "unknown"
		if workKB > 0 {
			current = fmt.Sprintf("%.0f MB", workKB/1024)
		}
		out = append(out, repository.RecommendationInput{
			Kind: KindTempSpill, Key: fmt.Sprint(s.IdentityID), Severity: "info",
			Title:   fmt.Sprintf("Statement %d spills %.0f MB to disk per call", s.Key.QueryID, spillMB),
			Summary: fmt.Sprintf("Sorts or hashes of this statement do not fit in work_mem (%s) and write temporary files. More work_mem for this statement or its role (%s) keeps them in memory.", current, s.UserName),
			Details: map[string]any{"statement": r, "temp_mb_per_call": spillMB, "work_mem_kb": workKB, "suggested_mb": suggestMB, "role": s.UserName},
			DDL:     &ddl, Benefit: benefit(s.Share*60, 3),
			Confidence: Confidence{Level: "medium", Reasons: []string{"temporary blocks written per call from pg_stat_statements",
				"the suggestion is an estimate: memory per sort can exceed the spilled size, and every sort/hash node of every session can use work_mem"}},
		})
	}
	return out
}

// benefit is a 0-100 ordering score with a floor for structural issues.
func benefit(v, floor float64) float64 {
	return math.Round(math.Min(100, math.Max(v, floor))*10) / 10
}

func compact(v float64) string {
	switch {
	case v >= 1e9:
		return fmt.Sprintf("%.1fB", v/1e9)
	case v >= 1e6:
		return fmt.Sprintf("%.1fM", v/1e6)
	case v >= 1e3:
		return fmt.Sprintf("%.1fk", v/1e3)
	}
	return fmt.Sprintf("%.0f", v)
}

func bytesLabel(b int64) string {
	switch {
	case b >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(b)/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%.0f MiB", float64(b)/(1<<20))
	}
	return fmt.Sprintf("%d KiB", b>>10)
}

var plainIdent = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// quoteIdent quotes an identifier when PostgreSQL needs it to.
func quoteIdent(s string) string {
	if plainIdent.MatchString(s) && !reserved[s] {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func quoteQualified(schema, name string) string { return quoteIdent(schema) + "." + quoteIdent(name) }

func quoteList(cols []string) string {
	q := make([]string, len(cols))
	for i, c := range cols {
		q[i] = quoteIdent(c)
	}
	return strings.Join(q, ", ")
}

// reserved are SQL keywords that cannot be bare identifiers.
var reserved = map[string]bool{"all": true, "analyse": true, "analyze": true, "and": true, "any": true, "array": true, "as": true, "asc": true,
	"asymmetric": true, "both": true, "case": true, "cast": true, "check": true, "collate": true, "column": true, "constraint": true,
	"create": true, "current_date": true, "current_role": true, "current_time": true, "current_timestamp": true, "current_user": true,
	"default": true, "deferrable": true, "desc": true, "distinct": true, "do": true, "else": true, "end": true, "except": true, "false": true,
	"fetch": true, "for": true, "foreign": true, "from": true, "grant": true, "group": true, "having": true, "in": true, "initially": true,
	"intersect": true, "into": true, "lateral": true, "leading": true, "limit": true, "localtime": true, "localtimestamp": true, "not": true,
	"null": true, "offset": true, "on": true, "only": true, "or": true, "order": true, "placing": true, "primary": true, "references": true,
	"returning": true, "select": true, "session_user": true, "some": true, "symmetric": true, "table": true, "then": true, "to": true,
	"trailing": true, "true": true, "union": true, "unique": true, "user": true, "using": true, "variadic": true, "when": true, "where": true,
	"window": true, "with": true}
