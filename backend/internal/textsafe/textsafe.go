// Package textsafe makes server-provided strings safe to store and print.
//
// Values such as application_name and query text are chosen by arbitrary
// database clients, so they may contain terminal escape sequences or
// bidirectional-override characters that would let a client rewrite what an
// operator sees in their terminal.
package textsafe

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Truncate shortens s to at most maxRunes runes without splitting a UTF-8
// sequence. It reports whether s was shortened. maxRunes <= 0 means no limit.
// Invalid UTF-8 bytes are replaced with U+FFFD so the result is always valid.
func Truncate(s string, maxRunes int) (string, bool) {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if maxRunes <= 0 || utf8.RuneCountInString(s) <= maxRunes {
		return s, false
	}
	n := 0
	for i := range s {
		if n == maxRunes {
			return s[:i], true
		}
		n++
	}
	return s, false
}

// Terminal returns s with every character that could alter terminal state
// replaced by a visible escape: C0 and C1 control characters (including ESC),
// DEL, and Unicode bidirectional formatting characters. Tabs and newlines are
// replaced by a single space so a value always stays on one line.
func Terminal(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteByte(' ')
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f):
			fmt.Fprintf(&b, "\\x%02x", r)
		case isBidiControl(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// isBidiControl reports Unicode bidirectional formatting characters
// (the "Trojan Source" set).
func isBidiControl(r rune) bool {
	switch {
	case r == 0x061c, r == 0x200e, r == 0x200f:
		return true
	case r >= 0x202a && r <= 0x202e:
		return true
	case r >= 0x2066 && r <= 0x2069:
		return true
	}
	return false
}
