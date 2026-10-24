// Package render writes reports as human-readable text or JSON.
//
// Every string that originates from the server (names, states, query text)
// passes through textsafe.Terminal before it reaches a terminal, so a client
// cannot inject escape sequences into an operator's screen. Text output uses
// no colours or cursor control, so it reads the same when redirected.
package render

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/locklens/locklens/internal/model"
	"github.com/locklens/locklens/internal/report"
	"github.com/locklens/locklens/internal/textsafe"
)

const unknown = "unknown"

// Table writes the human-readable report.
func Table(w io.Writer, r *report.Report) error {
	p := &printer{w: w, r: r}
	p.header()
	p.summary()
	p.chains()
	p.blocked()
	p.longTransactions()
	p.queries()
	p.warnings()
	return p.err
}

type printer struct {
	w        io.Writer
	r        *report.Report
	err      error
	sessions map[int]*model.Session
	ages     map[int]report.SessionAges
	nodes    map[int]report.Node
}

func (p *printer) printf(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

func (p *printer) session(pid int) (*model.Session, bool) {
	if p.sessions == nil {
		p.sessions = map[int]*model.Session{}
		for i := range p.r.Snapshot.Sessions {
			p.sessions[p.r.Snapshot.Sessions[i].PID] = &p.r.Snapshot.Sessions[i]
		}
		p.ages = map[int]report.SessionAges{}
		for _, a := range p.r.Analysis.Sessions {
			p.ages[a.PID] = a
		}
		p.nodes = map[int]report.Node{}
		for _, n := range p.r.Analysis.Nodes {
			p.nodes[n.ID] = n
		}
	}
	s, ok := p.sessions[pid]
	return s, ok
}

func (p *printer) age(pid int) report.SessionAges {
	p.session(pid)
	return p.ages[pid]
}

func (p *printer) node(pid int) report.Node {
	p.session(pid)
	return p.nodes[pid]
}

func (p *printer) header() {
	s := p.r.Snapshot
	p.printf("LockLens report\n")
	p.printf("  Observed at:  %s (server clock)\n", s.ServerTime.UTC().Format("2006-01-02 15:04:05 MST"))
	p.printf("  Target:       %s\n", target(s.Server))
	p.printf("  Server:       PostgreSQL %s\n", clean(s.Server.Version))
	p.printf("  Collection:   %d ms from several catalog reads (not an atomic snapshot)\n", s.Collection.DurationMS)
	p.printf("\n")
}

func target(srv model.Server) string {
	host := srv.Host
	if strings.HasPrefix(host, "/") {
		host = "socket " + host
	}
	if host == "" {
		host = unknown
	}
	return clean(fmt.Sprintf("%s:%d/%s as %s", host, srv.Port, srv.Database, srv.User))
}

func (p *printer) summary() {
	a := p.r.Analysis
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	p.printf("Summary\n")
	rows := [][2]string{
		{"Blocked sessions:", fmt.Sprint(a.Summary.BlockedSessions)},
		{"Blocking sessions:", fmt.Sprint(a.Summary.BlockingSessions)},
		{"Root blockers:", fmt.Sprint(a.Summary.RootBlockers)},
	}
	if a.Summary.CyclicComponents > 0 {
		rows = append(rows, [2]string{"Cyclic incidents:", fmt.Sprint(a.Summary.CyclicComponents)})
	}
	rows = append(rows,
		[2]string{fmt.Sprintf("Long transactions (>= %s):", Duration(a.MinTxAgeSeconds)), fmt.Sprint(a.Summary.LongTransactions)},
		[2]string{"  of which idle in transaction:", fmt.Sprint(a.Summary.IdleInTransaction)},
	)
	for _, row := range rows {
		if p.err == nil {
			_, p.err = fmt.Fprintf(tw, "  %s\t%s\n", row[0], row[1])
		}
	}
	if p.err == nil {
		p.err = tw.Flush()
	}
	p.printf("\n")
}

func (p *printer) chains() {
	a := p.r.Analysis
	p.printf("Blocking chains (blocker -> waiter)\n")
	if len(a.Components) == 0 {
		p.printf("  No blocking detected: no session is waiting for a lock held by another session.\n\n")
		return
	}
	for _, c := range a.Components {
		var starts []int
		switch {
		case !c.Cyclic:
			starts = c.Roots
			for _, root := range c.Roots {
				n := p.node(root)
				p.printf("  Incident %d: root blocker %s blocks %d directly, %d in total\n",
					c.ID, p.label(root), n.DirectBlocked, n.TransitiveBlocked)
			}
		default:
			p.printf("  Incident %d: CYCLE among PIDs %s (no root blocker reported; not a confirmed deadlock)\n",
				c.ID, ints(c.CycleMembers))
			starts = c.Entries
			if len(starts) == 0 {
				starts = c.CycleMembers[:1]
			}
		}
		shown := map[int]bool{}
		for _, s := range starts {
			p.tree(s, "    ", "", shown, map[int]bool{})
		}
		p.printf("\n")
	}
}

// tree prints the subtree under pid. shown holds nodes already printed in
// this incident (shared descendants are printed once and referenced
// afterwards); path holds the current ancestry and detects cycles. Both
// guarantee termination on any input.
func (p *printer) tree(pid int, indent, branch string, shown, path map[int]bool) {
	switch {
	case path[pid]:
		p.printf("%s%s%s (cycle back to an ancestor)\n", indent, branch, p.label(pid))
		return
	case shown[pid]:
		p.printf("%s%s%s (shown above)\n", indent, branch, p.label(pid))
		return
	}
	shown[pid] = true
	path[pid] = true
	defer delete(path, pid)

	p.printf("%s%s%s\n", indent, branch, p.describe(pid))
	childIndent := indent
	switch branch {
	case "├─ ":
		childIndent += "│  "
	case "└─ ":
		childIndent += "   "
	}
	for _, line := range p.waitingFor(pid) {
		p.printf("%s   %s\n", childIndent, line)
	}
	children := p.node(pid).Blocks
	for i, child := range children {
		b := "├─ "
		if i == len(children)-1 {
			b = "└─ "
		}
		p.tree(child, childIndent, b, shown, path)
	}
}

func (p *printer) label(pid int) string {
	if pid == 0 {
		return "prepared transaction (PID 0)"
	}
	return fmt.Sprintf("pid %d", pid)
}

func (p *printer) describe(pid int) string {
	n := p.node(pid)
	parts := []string{p.label(pid)}
	switch n.Kind {
	case report.KindPreparedTransaction:
		parts = append(parts, fmt.Sprintf("[%d prepared transaction(s) exist; PostgreSQL does not say which one blocks]",
			len(p.r.Snapshot.PreparedTransactions)))
	case report.KindMissing:
		parts = append(parts, "[no pg_stat_activity row: the session may have ended during collection]")
	default:
		s, _ := p.session(pid)
		ages := p.age(pid)
		if !s.DetailsVisible {
			parts = append(parts, "[details hidden by permissions]")
		}
		parts = append(parts, str(s.State))
		if len(s.WaitingLocks) > 0 || len(n.BlockedBy) > 0 {
			parts = append(parts, "lock wait "+age(ages.LockWaitSeconds))
		}
		parts = append(parts, "tx "+age(ages.TransactionAgeSeconds))
		parts = append(parts, "user="+str(s.User), "app="+short(s.ApplicationName, 32))
		if s.ParallelWorkers > 0 {
			parts = append(parts, fmt.Sprintf("parallel workers=%d", s.ParallelWorkers))
		}
	}
	if n.DirectBlocked > 0 {
		parts = append(parts, fmt.Sprintf("blocks %d directly, %d in total", n.DirectBlocked, n.TransitiveBlocked))
	}
	return strings.Join(parts, "  ")
}

func (p *printer) waitingFor(pid int) []string {
	s, ok := p.session(pid)
	if !ok {
		return nil
	}
	var out []string
	for _, l := range s.WaitingLocks {
		out = append(out, "waiting for "+DescribeLock(l))
	}
	return out
}

func (p *printer) blocked() {
	var rows [][]string
	for _, n := range p.r.Analysis.Nodes {
		if len(n.BlockedBy) == 0 {
			continue
		}
		row := []string{fmt.Sprint(n.ID), ints(n.BlockedBy)}
		if s, ok := p.session(n.ID); ok {
			ages := p.age(n.ID)
			row = append(row, str(s.State), waitEvent(s), age(ages.LockWaitSeconds), age(ages.TransactionAgeSeconds),
				age(ages.QueryAgeSeconds), str(s.User), short(s.ApplicationName, 24))
		} else {
			row = append(row, unknown, unknown, unknown, unknown, unknown, unknown, unknown)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return
	}
	p.printf("Blocked sessions\n")
	p.table([]string{"PID", "BLOCKED BY", "STATE", "WAIT EVENT", "LOCK WAIT", "TX AGE", "QUERY AGE", "USER", "APPLICATION"}, rows)
	p.printf("  QUERY AGE is the age of the current statement, not the lock wait.\n\n")
}

func (p *printer) longTransactions() {
	a := p.r.Analysis
	p.printf("Long-running transactions (>= %s)\n", Duration(a.MinTxAgeSeconds))
	if len(a.LongTransactions) == 0 {
		p.printf("  None.\n\n")
		return
	}
	var rows [][]string
	for _, lt := range a.LongTransactions {
		s, _ := p.session(lt.PID)
		ages := p.age(lt.PID)
		state := str(lt.State)
		if lt.IdleInTransaction {
			state += " (!)"
		}
		blocks := "-"
		if lt.DirectBlocked > 0 {
			blocks = fmt.Sprint(lt.DirectBlocked)
		}
		rows = append(rows, []string{fmt.Sprint(lt.PID), state, Duration(lt.TransactionAgeSeconds), age(ages.StateAgeSeconds),
			blocks, str(s.User), str(s.Database), short(s.ApplicationName, 24), str(s.ClientAddr)})
	}
	p.table([]string{"PID", "STATE", "TX AGE", "IN STATE FOR", "BLOCKS", "USER", "DATABASE", "APPLICATION", "CLIENT"}, rows)
	if a.Summary.IdleInTransaction > 0 {
		p.printf("  (!) idle in transaction: the transaction is open but no statement is running.\n")
	}
	p.printf("\n")
}

func (p *printer) queries() {
	snap := p.r.Snapshot
	if !snap.Collection.IncludeQuery {
		return
	}
	interesting := map[int]bool{}
	for _, n := range p.r.Analysis.Nodes {
		interesting[n.ID] = true
	}
	for _, lt := range p.r.Analysis.LongTransactions {
		interesting[lt.PID] = true
	}
	var pids []int
	for pid := range interesting {
		pids = append(pids, pid)
	}
	slices.Sort(pids)
	p.printf("Query text (opt-in, up to %d characters each)\n", snap.Collection.QueryMaxLength)
	printed := 0
	for _, pid := range pids {
		s, ok := p.session(pid)
		if !ok {
			continue
		}
		switch {
		case s.Query != nil:
			suffix := ""
			if s.QueryTruncated {
				suffix = " [truncated]"
			}
			p.printf("  pid %d: %s%s\n", pid, textsafe.Terminal(*s.Query), suffix)
		case !s.DetailsVisible:
			p.printf("  pid %d: [hidden by permissions]\n", pid)
		default:
			p.printf("  pid %d: [not available]\n", pid)
		}
		printed++
	}
	if printed == 0 {
		p.printf("  No sessions of interest.\n")
	}
	p.printf("\n")
}

func (p *printer) warnings() {
	all := append(slices.Clone(p.r.Snapshot.Warnings), p.r.Analysis.Warnings...)
	if len(all) == 0 {
		return
	}
	p.printf("Warnings\n")
	for _, w := range all {
		p.printf("  - [%s] %s\n", w.Code, clean(w.Message))
	}
	p.printf("\n")
}

func (p *printer) table(header []string, rows [][]string) {
	if p.err != nil {
		return
	}
	tw := tabwriter.NewWriter(p.w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "  %s\n", strings.Join(header, "\t"))
	for _, r := range rows {
		fmt.Fprintf(tw, "  %s\n", strings.Join(r, "\t"))
	}
	p.err = tw.Flush()
}

// DescribeLock renders an ungranted lock request in plain words. It states
// what was requested and never claims which held lock conflicts with it.
func DescribeLock(l model.WaitingLock) string {
	rel := unknown
	switch {
	case l.Relation != nil:
		rel = clean(*l.Relation)
	case l.RelationOID != nil:
		rel = fmt.Sprintf("relation OID %d", *l.RelationOID)
		if l.Database != nil {
			rel += " in database " + clean(*l.Database)
		}
	}
	mode := clean(l.Mode)
	switch l.LockType {
	case "relation":
		return fmt.Sprintf("%s on relation %s", mode, rel)
	case "tuple":
		return fmt.Sprintf("%s on tuple (%s,%s) of %s", mode, i32(l.Page), i16(l.Tuple), rel)
	case "page":
		return fmt.Sprintf("%s on page %s of %s", mode, i32(l.Page), rel)
	case "extend":
		return fmt.Sprintf("%s to extend %s", mode, rel)
	case "transactionid":
		return fmt.Sprintf("%s on transaction %s (typically a row lock held by that transaction)", mode, str(l.TransactionID))
	case "virtualxid":
		return fmt.Sprintf("%s on virtual transaction %s", mode, str(l.VirtualXID))
	case "advisory":
		return fmt.Sprintf("%s on advisory lock (%s, %s, %s)", mode, u32(l.ClassID), u32(l.ObjID), i16(l.ObjSubID))
	case "object":
		return fmt.Sprintf("%s on object (classid %s, objid %s, objsubid %s)", mode, u32(l.ClassID), u32(l.ObjID), i16(l.ObjSubID))
	default:
		return fmt.Sprintf("%s on %s lock", mode, clean(l.LockType))
	}
}

func waitEvent(s *model.Session) string {
	if s.WaitEventType == nil && s.WaitEvent == nil {
		if !s.DetailsVisible {
			return unknown
		}
		return "-"
	}
	return str(s.WaitEventType) + ":" + str(s.WaitEvent)
}

// Duration formats seconds compactly and stably: 850ms, 42s, 3m07s, 2h05m,
// 3d04h.
func Duration(seconds float64) string {
	d := time.Duration(seconds * float64(time.Second))
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func age(seconds *float64) string {
	if seconds == nil {
		return unknown
	}
	return Duration(*seconds)
}

func clean(s string) string { return textsafe.Terminal(s) }

func str(s *string) string {
	if s == nil {
		return unknown
	}
	if *s == "" {
		return "-"
	}
	return clean(*s)
}

func short(s *string, n int) string {
	if s == nil {
		return unknown
	}
	if *s == "" {
		return "-"
	}
	t, cut := textsafe.Truncate(*s, n)
	if cut {
		t += "…"
	}
	return clean(t)
}

func ints(ids []int) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ",")
}

func i32(v *int32) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprint(*v)
}

func i16(v *int16) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprint(*v)
}

func u32(v *uint32) string {
	if v == nil {
		return "?"
	}
	return fmt.Sprint(*v)
}
