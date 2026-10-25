package observe

import "strings"

// Statement command classes. They are bounded values, safe for grouping and
// storage without the statement text.
const (
	CommandSelect      = "select"
	CommandInsert      = "insert"
	CommandUpdate      = "update"
	CommandDelete      = "delete"
	CommandMerge       = "merge"
	CommandCopy        = "copy"
	CommandDDL         = "ddl"
	CommandTransaction = "transaction"
	CommandMaintenance = "maintenance"
	CommandUtility     = "utility"
	CommandOther       = "other"
)

// Commands lists the classes in display order.
var Commands = []string{CommandSelect, CommandInsert, CommandUpdate, CommandDelete, CommandMerge, CommandCopy,
	CommandDDL, CommandTransaction, CommandMaintenance, CommandUtility, CommandOther}

var leadingKeywords = map[string]string{
	"select": CommandSelect, "table": CommandSelect, "values": CommandSelect, "fetch": CommandSelect,
	"insert": CommandInsert, "update": CommandUpdate, "delete": CommandDelete, "merge": CommandMerge,
	"copy":   CommandCopy,
	"create": CommandDDL, "alter": CommandDDL, "drop": CommandDDL, "truncate": CommandDDL, "grant": CommandDDL,
	"revoke": CommandDDL, "comment": CommandDDL, "security": CommandDDL, "refresh": CommandDDL, "import": CommandDDL,
	"begin": CommandTransaction, "start": CommandTransaction, "commit": CommandTransaction, "end": CommandTransaction,
	"rollback": CommandTransaction, "abort": CommandTransaction, "savepoint": CommandTransaction,
	"release": CommandTransaction, "prepare": CommandTransaction,
	"vacuum": CommandMaintenance, "analyze": CommandMaintenance, "analyse": CommandMaintenance,
	"reindex": CommandMaintenance, "cluster": CommandMaintenance, "checkpoint": CommandMaintenance,
	"set": CommandUtility, "show": CommandUtility, "reset": CommandUtility, "explain": CommandUtility,
	"discard": CommandUtility, "listen": CommandUtility, "notify": CommandUtility, "unlisten": CommandUtility,
	"lock": CommandUtility, "declare": CommandUtility, "close": CommandUtility, "move": CommandUtility,
	"deallocate": CommandUtility, "execute": CommandUtility, "load": CommandUtility, "call": CommandUtility,
	"do": CommandUtility,
}

// ClassifyCommand returns the command class of a (normalised) SQL statement
// from its leading keyword. A WITH query is classified by its main statement:
// the first data-modifying keyword at parenthesis depth zero after the common
// table expressions, else select. Comments and string literals are skipped.
// It is a lexical heuristic for grouping, not a parser.
func ClassifyCommand(sql string) string {
	toks := keywords(sql, 64)
	if len(toks) == 0 {
		return CommandOther
	}
	first := toks[0].word
	switch first {
	case "with":
		for _, t := range toks[1:] {
			if t.depth != 0 {
				continue
			}
			switch t.word {
			case "insert", "update", "delete", "merge":
				return leadingKeywords[t.word]
			case "select", "table", "values":
				return CommandSelect
			}
		}
		return CommandSelect
	case "prepare":
		// PREPARE name AS <statement> is classified by the statement;
		// PREPARE TRANSACTION is two-phase commit.
		if len(toks) > 1 && toks[1].word == "transaction" {
			return CommandTransaction
		}
		for i, t := range toks {
			if t.word == "as" && i+1 < len(toks) {
				if c, ok := leadingKeywords[toks[i+1].word]; ok {
					return c
				}
			}
		}
		return CommandUtility
	}
	if c, ok := leadingKeywords[first]; ok {
		return c
	}
	return CommandOther
}

type token struct {
	word  string
	depth int
}

// keywords returns up to max lower-cased bare words with their parenthesis
// depth, skipping comments, quoted identifiers, and string literals.
func keywords(s string, max int) []token {
	var out []token
	depth := 0
	for i := 0; i < len(s) && len(out) < max; {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			end := strings.Index(s[i+2:], "*/")
			if end < 0 {
				return out
			}
			i += end + 4
		case c == '\'' || c == '"':
			i++
			for i < len(s) {
				if s[i] == c {
					if i+1 < len(s) && s[i+1] == c { // doubled quote
						i += 2
						continue
					}
					break
				}
				i++
			}
			i++
		case c == '$':
			// Dollar-quoted string ($tag$...$tag$) or a parameter ($1).
			j := i + 1
			for j < len(s) && (isWordByte(s[j]) && !(s[j] >= '0' && s[j] <= '9' && j == i+1)) {
				j++
			}
			if j < len(s) && s[j] == '$' {
				tag := s[i : j+1]
				end := strings.Index(s[j+1:], tag)
				if end < 0 {
					return out
				}
				i = j + 1 + end + len(tag)
			} else {
				i = j
				for i < len(s) && s[i] >= '0' && s[i] <= '9' {
					i++
				}
			}
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case isWordByte(c) && !(c >= '0' && c <= '9'):
			j := i
			for j < len(s) && isWordByte(s[j]) {
				j++
			}
			out = append(out, token{word: strings.ToLower(s[i:j]), depth: depth})
			i = j
		default:
			i++
		}
	}
	return out
}

func isWordByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c >= 0x80
}
