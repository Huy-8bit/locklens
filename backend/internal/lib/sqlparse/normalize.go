package sqlparse

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strconv"
	"strings"
)

// Normalize replaces the constants of a statement with $1, $2, … (as
// pg_stat_statements does) and returns a fingerprint that is the same for
// statements that differ only in constants. It uses PostgreSQL's parser
// when the build has it, and otherwise (or when the parser fails) a lexical
// pass that replaces string, numeric, and dollar-quoted literals.
func Normalize(sql string) (normalized, fingerprint string) {
	if n, fp, ok := normalizeParsed(sql); ok {
		return n, fp
	}
	n := NormalizeLexically(sql)
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(strings.ToLower(n)), " ")))
	return n, "lex:" + hex.EncodeToString(sum[:8])
}

// NormalizeLexically replaces literals without parsing: quoted strings
// ('…', E'…', B'…', X'…', U&'…'), dollar-quoted strings, and numbers that are
// not part of a name. Quoted identifiers ("…") are kept.
func NormalizeLexically(sql string) string {
	var b strings.Builder
	// Numbering continues after the statement's own parameters.
	n := 0
	for _, m := range paramRe.FindAllStringSubmatch(sql, -1) {
		if v, err := strconv.Atoi(m[1]); err == nil && v > n {
			n = v
		}
	}
	param := func() {
		n++
		b.WriteString("$" + strconv.Itoa(n))
	}
	for i := 0; i < len(sql); {
		c := sql[i]
		switch {
		case c == '-' && i+1 < len(sql) && sql[i+1] == '-': // comment to end of line
			j := strings.IndexByte(sql[i:], '\n')
			if j < 0 {
				i = len(sql)
			} else {
				i += j
			}
		case c == '/' && i+1 < len(sql) && sql[i+1] == '*':
			j := strings.Index(sql[i+2:], "*/")
			if j < 0 {
				i = len(sql)
			} else {
				i += j + 4
			}
		case c == '"':
			j := i + 1
			for j < len(sql) && (sql[j] != '"' || j+1 < len(sql) && sql[j+1] == '"') {
				if sql[j] == '"' {
					j++
				}
				j++
			}
			end := min(j+1, len(sql))
			b.WriteString(sql[i:end])
			i = end
		case c == '\'' || (c == 'E' || c == 'e' || c == 'B' || c == 'b' || c == 'X' || c == 'x') && i+1 < len(sql) && sql[i+1] == '\'' && !identByte(prev(sql, i)):
			if c != '\'' {
				i++
			}
			j := i + 1
			for j < len(sql) {
				if sql[j] == '\\' && c != '\'' {
					j += 2
					continue
				}
				if sql[j] == '\'' {
					if j+1 < len(sql) && sql[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			param()
			i = min(j+1, len(sql))
		case c == '$' && i+1 < len(sql) && (sql[i+1] == '$' || isIdentStart(sql[i+1])):
			// $tag$…$tag$, or a parameter ($1) which stays.
			j := i + 1
			for j < len(sql) && (isIdentStart(sql[j]) || sql[j] >= '0' && sql[j] <= '9') {
				j++
			}
			if j < len(sql) && sql[j] == '$' {
				tag := sql[i : j+1]
				end := strings.Index(sql[j+1:], tag)
				if end < 0 {
					end = len(sql) - j - 1
				} else {
					end += len(tag)
				}
				param()
				i = j + 1 + end
				continue
			}
			b.WriteString(sql[i:j])
			i = j
		case c >= '0' && c <= '9' && !identByte(prev(sql, i)) && prev(sql, i) != '$':
			j := i
			for j < len(sql) && (sql[j] >= '0' && sql[j] <= '9' || sql[j] == '.' || sql[j] == '_' || sql[j] == 'e' || sql[j] == 'E' ||
				(sql[j] == '-' || sql[j] == '+') && (sql[j-1] == 'e' || sql[j-1] == 'E')) {
				j++
			}
			param()
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

var paramRe = regexp.MustCompile(`\$(\d+)`)

func prev(s string, i int) byte {
	if i == 0 {
		return ' '
	}
	return s[i-1]
}

func identByte(c byte) bool { return isIdentStart(c) || c >= '0' && c <= '9' || c == '$' }

func isIdentStart(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}
