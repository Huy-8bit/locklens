//go:build cgo

package sqlparse

import (
	"fmt"
	"strings"

	pg "github.com/pganalyze/pg_query_go/v6"
)

const available = true

// Analyze parses one SQL statement (for example a normalised
// pg_stat_statements text with $1 placeholders) and extracts its tables,
// predicates, sort order, and lints. Several statements are analysed as the
// first one, with a lint.
func Analyze(sql string) (Analysis, error) {
	tree, err := pg.Parse(sql)
	if err != nil {
		return Analysis{}, fmt.Errorf("parse: %w", err)
	}
	a := Analysis{Command: "other", Tables: []Table{}, Predicates: []Predicate{}, OrderBy: []OrderColumn{}, Lints: []Lint{}}
	if fp, err := pg.Fingerprint(sql); err == nil {
		a.Fingerprint = fp
	}
	stmts := tree.GetStmts()
	if len(stmts) == 0 {
		return a, nil
	}
	if len(stmts) > 1 {
		a.lint(LintMultiStatement, "info", "The text holds %d statements; only the first is analysed.", len(stmts))
	}
	w := &walker{a: &a}
	switch n := stmts[0].GetStmt(); {
	case n.GetSelectStmt() != nil:
		a.Command = "select"
		w.selectStmt(n.GetSelectStmt(), true)
	case n.GetUpdateStmt() != nil:
		a.Command = "update"
		u := n.GetUpdateStmt()
		scope := w.scope(append([]*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: u.GetRelation()}}}, u.GetFromClause()...))
		if u.GetWhereClause() == nil {
			a.lint(LintNoWhere, "warning", "UPDATE without WHERE changes every row of %s.", tableName(u.GetRelation()))
		}
		w.where(u.GetWhereClause(), scope, false)
	case n.GetDeleteStmt() != nil:
		a.Command = "delete"
		d := n.GetDeleteStmt()
		scope := w.scope(append([]*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: d.GetRelation()}}}, d.GetUsingClause()...))
		if d.GetWhereClause() == nil {
			a.lint(LintNoWhere, "warning", "DELETE without WHERE removes every row of %s.", tableName(d.GetRelation()))
		}
		w.where(d.GetWhereClause(), scope, false)
	case n.GetInsertStmt() != nil:
		a.Command = "insert"
		in := n.GetInsertStmt()
		w.scope([]*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: in.GetRelation()}}})
		if sel := in.GetSelectStmt().GetSelectStmt(); sel != nil && len(sel.GetFromClause()) > 0 {
			w.selectStmt(sel, false)
		}
	case n.GetMergeStmt() != nil:
		a.Command = "merge"
		m := n.GetMergeStmt()
		w.scope([]*pg.Node{{Node: &pg.Node_RangeVar{RangeVar: m.GetRelation()}}, m.GetSourceRelation()})
	}
	return a, nil
}

func (a *Analysis) lint(code, severity, format string, args ...any) {
	for _, l := range a.Lints {
		if l.Code == code {
			return
		}
	}
	a.Lints = append(a.Lints, Lint{Code: code, Severity: severity, Message: fmt.Sprintf(format, args...)})
}

type walker struct {
	a *Analysis
}

// scope maps aliases and names of the FROM items to qualified tables, and
// records the tables.
type scope struct {
	byAlias map[string]string
	tables  []string
}

// resolve returns the qualified table of a column reference.
func (s scope) resolve(qualifier string) string {
	if qualifier != "" {
		return s.byAlias[qualifier]
	}
	if len(s.tables) == 1 {
		return s.tables[0]
	}
	return ""
}

func (w *walker) scope(from []*pg.Node) scope {
	sc := scope{byAlias: map[string]string{}}
	var add func(n *pg.Node)
	add = func(n *pg.Node) {
		switch {
		case n.GetRangeVar() != nil:
			rv := n.GetRangeVar()
			t := Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname(), Alias: rv.GetAlias().GetAliasname()}
			q := t.Qualified()
			w.addTable(t)
			sc.tables = append(sc.tables, q)
			sc.byAlias[rv.GetRelname()] = q
			if t.Alias != "" {
				sc.byAlias[t.Alias] = q
			}
		case n.GetJoinExpr() != nil:
			j := n.GetJoinExpr()
			add(j.GetLarg())
			add(j.GetRarg())
		case n.GetRangeSubselect() != nil:
			if sel := n.GetRangeSubselect().GetSubquery().GetSelectStmt(); sel != nil {
				w.selectStmt(sel, false)
			}
		}
	}
	for _, n := range from {
		add(n)
	}
	// Join conditions are predicates too (an index on a join column helps a
	// nested loop).
	var quals func(n *pg.Node)
	quals = func(n *pg.Node) {
		if j := n.GetJoinExpr(); j != nil {
			quals(j.GetLarg())
			quals(j.GetRarg())
			w.where(j.GetQuals(), sc, false)
		}
	}
	for _, n := range from {
		quals(n)
	}
	return sc
}

func (w *walker) addTable(t Table) {
	for _, x := range w.a.Tables {
		if x.Qualified() == t.Qualified() {
			return
		}
	}
	w.a.Tables = append(w.a.Tables, t)
}

func (w *walker) selectStmt(s *pg.SelectStmt, top bool) {
	if s.GetLarg() != nil || s.GetRarg() != nil {
		if s.GetLarg() != nil {
			w.selectStmt(s.GetLarg(), false)
		}
		if s.GetRarg() != nil {
			w.selectStmt(s.GetRarg(), false)
		}
		return
	}
	for _, cte := range s.GetWithClause().GetCtes() {
		if sel := cte.GetCommonTableExpr().GetCtequery().GetSelectStmt(); sel != nil {
			w.selectStmt(sel, false)
		}
	}
	sc := w.scope(s.GetFromClause())
	if top {
		for _, t := range s.GetTargetList() {
			for _, f := range t.GetResTarget().GetVal().GetColumnRef().GetFields() {
				if f.GetAStar() != nil {
					w.a.lint(LintSelectStar, "info", "SELECT * reads every column: wider rows, no index-only scans, and breakage when columns change. List the columns the application uses.")
				}
			}
		}
		if s.GetLimitCount() != nil {
			w.a.HasLimit = true
		}
		if s.GetLimitOffset() != nil {
			w.a.lint(LintOffset, "info", "OFFSET pagination reads and discards every skipped row, so deep pages get slower. Keyset pagination (WHERE sort_key > last value) stays fast.")
		}
		for _, sb := range s.GetSortClause() {
			by := sb.GetSortBy()
			if col, qual, ok := columnRef(by.GetNode()); ok {
				w.a.OrderBy = append(w.a.OrderBy, OrderColumn{Table: sc.resolve(qual), Column: col, Desc: by.GetSortbyDir() == pg.SortByDir_SORTBY_DESC})
			}
		}
	}
	if len(s.GetFromClause()) > 1 && s.GetWhereClause() == nil {
		w.a.lint(LintCartesian, "warning", "Several tables in FROM without a WHERE clause: every row of one is combined with every row of the others.")
	}
	w.where(s.GetWhereClause(), sc, false)
}

// where collects the column predicates of a condition.
func (w *walker) where(n *pg.Node, sc scope, inOr bool) {
	if n == nil {
		return
	}
	switch {
	case n.GetBoolExpr() != nil:
		b := n.GetBoolExpr()
		or := b.GetBoolop() == pg.BoolExprType_OR_EXPR
		if or {
			cols := map[string]bool{}
			for _, arg := range b.GetArgs() {
				if col, _, ok := predicateColumn(arg); ok {
					cols[col] = true
				}
			}
			if len(cols) > 1 {
				w.a.lint(LintOrAcrossColumns, "info", "OR across different columns often prevents a single index scan; PostgreSQL may need a bitmap OR or a sequential scan. A UNION of two indexed queries can be faster.")
			}
		}
		for _, arg := range b.GetArgs() {
			w.where(arg, sc, inOr || or || b.GetBoolop() == pg.BoolExprType_NOT_EXPR)
		}
	case n.GetAExpr() != nil:
		w.aexpr(n.GetAExpr(), sc, inOr)
	case n.GetNullTest() != nil:
		nt := n.GetNullTest()
		if col, qual, ok := columnRef(nt.GetArg()); ok {
			op := "IS NULL"
			if nt.GetNulltesttype() == pg.NullTestType_IS_NOT_NULL {
				op = "IS NOT NULL"
			}
			w.add(Predicate{Table: sc.resolve(qual), Column: col, Op: op, InOr: inOr})
		}
	case n.GetSubLink() != nil:
		sl := n.GetSubLink()
		if sel := sl.GetSubselect().GetSelectStmt(); sel != nil {
			w.selectStmt(sel, false)
		}
		if sl.GetSubLinkType() == pg.SubLinkType_ANY_SUBLINK {
			if col, qual, ok := columnRef(sl.GetTestexpr()); ok {
				w.add(Predicate{Table: sc.resolve(qual), Column: col, Op: "IN", InOr: inOr})
			}
		}
	}
	// NOT IN (SELECT ...) is NOT over an ANY sublink.
	if b := n.GetBoolExpr(); b != nil && b.GetBoolop() == pg.BoolExprType_NOT_EXPR {
		for _, arg := range b.GetArgs() {
			if sl := arg.GetSubLink(); sl != nil && sl.GetSubLinkType() == pg.SubLinkType_ANY_SUBLINK {
				w.a.lint(LintNotInSubquery, "warning", "NOT IN (subquery) cannot use an anti-join and returns no rows if the subquery yields a NULL. NOT EXISTS is usually faster and correct.")
			}
		}
	}
}

func (w *walker) aexpr(e *pg.A_Expr, sc scope, inOr bool) {
	op := ""
	switch e.GetKind() {
	case pg.A_Expr_Kind_AEXPR_OP:
		op = operator(e)
	case pg.A_Expr_Kind_AEXPR_IN, pg.A_Expr_Kind_AEXPR_OP_ANY:
		op = "IN"
		if operator(e) == "<>" {
			return // NOT IN (list): no index can serve it
		}
	case pg.A_Expr_Kind_AEXPR_LIKE:
		op = "LIKE"
	case pg.A_Expr_Kind_AEXPR_ILIKE:
		op = "ILIKE"
	case pg.A_Expr_Kind_AEXPR_BETWEEN, pg.A_Expr_Kind_AEXPR_BETWEEN_SYM:
		op = "BETWEEN"
	default:
		return
	}
	switch op {
	case "=", "<", "<=", ">", ">=", "<>", "!=", "IN", "LIKE", "ILIKE", "BETWEEN":
	default:
		return
	}
	if op == "!=" {
		op = "<>"
	}
	lcol, lqual, lok := columnRef(e.GetLexpr())
	rcol, rqual, rok := columnRef(e.GetRexpr())
	switch {
	case lok && rok && op == "=":
		// A join condition: both sides are columns.
		lt, rt := sc.resolve(lqual), sc.resolve(rqual)
		if lt != rt {
			w.add(Predicate{Table: lt, Column: lcol, Op: "JOIN", InOr: inOr})
			w.add(Predicate{Table: rt, Column: rcol, Op: "JOIN", InOr: inOr})
			return
		}
	case lok:
		w.add(Predicate{Table: sc.resolve(lqual), Column: lcol, Op: op, InOr: inOr})
	case rok:
		w.add(Predicate{Table: sc.resolve(rqual), Column: rcol, Op: flip(op), InOr: inOr})
	default:
		if fn, col, qual, ok := wrappedColumn(e.GetLexpr()); ok {
			w.add(Predicate{Table: sc.resolve(qual), Column: col, Op: op, Wrapped: fn, InOr: inOr})
			w.a.lint(LintFunctionOnColumn, "warning", "%s(%s) in a condition cannot use a plain index on %s. An expression index on %s(%s) can.", fn, col, col, fn, col)
		}
	}
	if (op == "LIKE" || op == "ILIKE") && strings.HasPrefix(constString(e.GetRexpr()), "%") {
		w.a.lint(LintLeadingWildcard, "warning", "A pattern starting with %% cannot use a B-tree index. A trigram index (pg_trgm) or full-text search can.")
	}
}

func (w *walker) add(p Predicate) {
	for _, x := range w.a.Predicates {
		if x == p {
			return
		}
	}
	w.a.Predicates = append(w.a.Predicates, p)
}

func operator(e *pg.A_Expr) string {
	if len(e.GetName()) == 0 {
		return ""
	}
	return e.GetName()[len(e.GetName())-1].GetString_().GetSval()
}

func flip(op string) string {
	switch op {
	case "<":
		return ">"
	case "<=":
		return ">="
	case ">":
		return "<"
	case ">=":
		return "<="
	}
	return op
}

// columnRef returns the column and its qualifier (alias or table) of a
// column reference, looking through casts.
func columnRef(n *pg.Node) (col, qualifier string, ok bool) {
	if tc := n.GetTypeCast(); tc != nil {
		return columnRef(tc.GetArg())
	}
	cr := n.GetColumnRef()
	if cr == nil {
		return "", "", false
	}
	var parts []string
	for _, f := range cr.GetFields() {
		s := f.GetString_()
		if s == nil {
			return "", "", false // a.* or similar
		}
		parts = append(parts, s.GetSval())
	}
	switch len(parts) {
	case 1:
		return parts[0], "", true
	case 2:
		return parts[1], parts[0], true
	case 3:
		return parts[2], parts[1], true
	}
	return "", "", false
}

// wrappedColumn matches f(column) (one column argument).
func wrappedColumn(n *pg.Node) (fn, col, qualifier string, ok bool) {
	fc := n.GetFuncCall()
	if fc == nil || len(fc.GetArgs()) == 0 {
		return "", "", "", false
	}
	names := fc.GetFuncname()
	if len(names) == 0 {
		return "", "", "", false
	}
	fn = names[len(names)-1].GetString_().GetSval()
	for _, arg := range fc.GetArgs() {
		if c, q, ok := columnRef(arg); ok {
			return fn, c, q, true
		}
	}
	return "", "", "", false
}

func predicateColumn(n *pg.Node) (col, qualifier string, ok bool) {
	if e := n.GetAExpr(); e != nil {
		if c, q, ok := columnRef(e.GetLexpr()); ok {
			return c, q, true
		}
		return columnRef(e.GetRexpr())
	}
	return "", "", false
}

func constString(n *pg.Node) string {
	if tc := n.GetTypeCast(); tc != nil {
		return constString(tc.GetArg())
	}
	return n.GetAConst().GetSval().GetSval()
}

func tableName(rv *pg.RangeVar) string {
	return Table{Schema: rv.GetSchemaname(), Name: rv.GetRelname()}.Qualified()
}
