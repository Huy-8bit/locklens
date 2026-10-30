package pglog

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Prefix parses lines written with a log_line_prefix.
type Prefix struct {
	re     *regexp.Regexp
	fields []byte // the escape letter of each capture group
}

// DefaultPrefix is PostgreSQL's default log_line_prefix.
const DefaultPrefix = "%m [%p] "

// prefixPatterns are the patterns of the log_line_prefix escapes LockLens
// reads; other escapes match any text.
var prefixPatterns = map[byte]string{
	'm': `\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} [^ \]]+`,
	't': `\d{4}-\d\d-\d\d \d\d:\d\d:\d\d [^ \]]+`,
	'n': `\d+\.\d+`,
	'p': `\d+`,
	'l': `\d+`,
	'c': `[0-9a-f]+\.[0-9a-f]+`,
	'e': `[0-9A-Z]{5}`,
	'Q': `-?\d+`,
	'u': `.*?`, 'd': `.*?`, 'a': `.*?`, 'h': `.*?`, 'r': `.*?`, 'b': `.*?`, 'i': `.*?`, 'v': `.*?`, 'x': `.*?`, 's': `.*?`,
	'L': `.*?`, 'P': `.*?`, 'q': ``,
}

// CompilePrefix compiles a log_line_prefix (for example "%m [%p] ").
func CompilePrefix(prefix string) (*Prefix, error) {
	var b strings.Builder
	p := &Prefix{}
	b.WriteString(`^`)
	optional := false
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		if c != '%' || i+1 == len(prefix) {
			b.WriteString(regexp.QuoteMeta(string(c)))
			continue
		}
		i++
		e := prefix[i]
		// A padding width (%-10u) is allowed and ignored.
		for e == '-' || e >= '0' && e <= '9' {
			if i+1 == len(prefix) {
				break
			}
			i++
			e = prefix[i]
		}
		switch {
		case e == '%':
			b.WriteString(`%`)
		case e == 'q':
			// What follows %q is written only by session processes.
			b.WriteString(`(?:`)
			optional = true
		default:
			pat, ok := prefixPatterns[e]
			if !ok {
				return nil, fmt.Errorf("log_line_prefix: unknown escape %%%c", e)
			}
			b.WriteString(` *(` + pat + `) *`)
			p.fields = append(p.fields, e)
		}
	}
	if optional {
		b.WriteString(`)?`)
	}
	b.WriteString(`(LOG|ERROR|FATAL|PANIC|WARNING|NOTICE|INFO|DEBUG[1-5]?|DETAIL|HINT|CONTEXT|STATEMENT|QUERY|LOCATION):  ?`)
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, fmt.Errorf("log_line_prefix: %w", err)
	}
	p.re = re
	return p, nil
}

// line is one parsed stderr line.
type line struct {
	rec  Record // prefix fields
	kind string // severity, or DETAIL, HINT, ...
	text string
}

func (p *Prefix) parse(s string) (line, bool) {
	m := p.re.FindStringSubmatch(s)
	if m == nil {
		return line{}, false
	}
	var l line
	for i, f := range p.fields {
		v := m[i+1]
		switch f {
		case 'm', 't':
			l.rec.Time = parseTime(v)
		case 'n':
			if sec, err := strconv.ParseFloat(v, 64); err == nil {
				l.rec.Time = unixFloat(sec)
			}
		case 'p':
			l.rec.PID, _ = strconv.Atoi(v)
		case 'l':
			l.rec.SessionLine, _ = strconv.ParseInt(v, 10, 64)
		case 'c':
			l.rec.SessionID = v
		case 'e':
			l.rec.SQLState = v
		case 'Q':
			l.rec.QueryID, _ = strconv.ParseInt(v, 10, 64)
		case 'u':
			l.rec.User = strings.TrimSpace(v)
		case 'd':
			l.rec.Database = strings.TrimSpace(v)
		case 'a':
			l.rec.Application = strings.TrimSpace(v)
		case 'b':
			l.rec.BackendType = strings.TrimSpace(v)
		}
	}
	l.kind = m[len(p.fields)+1]
	l.text = s[len(m[0]):]
	return l, true
}

// StderrReader assembles stderr log lines into records: a message line
// starts a record; DETAIL, HINT, CONTEXT, STATEMENT lines and continuation
// lines (starting with a tab) belong to the record of the same process.
type StderrReader struct {
	prefix *Prefix
	cur    *Record
	field  *string
}

// NewStderrReader reads stderr logs written with prefix.
func NewStderrReader(prefix *Prefix) *StderrReader { return &StderrReader{prefix: prefix} }

// Add reads one line (without its newline) and returns the records it
// completed.
func (s *StderrReader) Add(text string) []Record {
	if strings.HasPrefix(text, "\t") {
		if s.field != nil {
			*s.field += "\n" + text[1:]
		}
		return nil
	}
	l, ok := s.prefix.parse(text)
	if !ok {
		return nil // not a log line (output of a program, for example)
	}
	if severities[l.kind] {
		done := s.Flush()
		r := l.rec
		r.Severity, r.Message = l.kind, l.text
		s.cur = &r
		s.field = &s.cur.Message
		return done
	}
	if s.cur == nil || l.rec.PID != 0 && s.cur.PID != 0 && l.rec.PID != s.cur.PID {
		s.field = nil
		return nil
	}
	switch l.kind {
	case "DETAIL":
		s.field = &s.cur.Detail
	case "HINT":
		s.field = &s.cur.Hint
	case "CONTEXT":
		s.field = &s.cur.Context
	case "STATEMENT":
		s.field = &s.cur.Statement
	default:
		s.field = nil
		return nil
	}
	*s.field = l.text
	return nil
}

// Flush returns the record being assembled (at the end of input, or when
// no line followed for a while).
func (s *StderrReader) Flush() []Record {
	if s.cur == nil {
		return nil
	}
	r := *s.cur
	s.cur, s.field = nil, nil
	return []Record{r}
}
