package textsafe

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateCountsRunes(t *testing.T) {
	// Non-ASCII samples exercise two-, three-, and four-byte UTF-8 sequences.
	tests := []struct {
		in        string
		max       int
		want      string
		truncated bool
	}{
		{"hello", 10, "hello", false},
		{"hello", 5, "hello", false},
		{"hello", 3, "hel", true},
		{"héllo wörld", 4, "héll", true},
		{"日本語のクエリ", 3, "日本語", true},
		{"emoji 👍🏽 ok", 7, "emoji 👍", true},
		{"anything", 0, "anything", false},
	}
	for _, tt := range tests {
		got, truncated := Truncate(tt.in, tt.max)
		if got != tt.want || truncated != tt.truncated {
			t.Errorf("Truncate(%q, %d) = %q, %v; want %q, %v", tt.in, tt.max, got, truncated, tt.want, tt.truncated)
		}
		if !utf8.ValidString(got) {
			t.Errorf("Truncate(%q, %d) produced invalid UTF-8", tt.in, tt.max)
		}
	}
}

func TestTruncateRepairsInvalidUTF8(t *testing.T) {
	got, _ := Truncate("ab\xffcd", 10)
	if !utf8.ValidString(got) || got != "ab\uFFFDcd" {
		t.Fatalf("got %q", got)
	}
}

func TestTerminalNeutralisesControlSequences(t *testing.T) {
	in := "app\x1b[2J\x1b]0;pwned\x07name\u009b31m\x7f"
	got := Terminal(in)
	for _, bad := range []string{"\x1b", "\x07", "\u009b", "\x7f"} {
		if strings.Contains(got, bad) {
			t.Fatalf("Terminal(%q) = %q still contains %q", in, got, bad)
		}
	}
	if want := `app\x1b[2J\x1b]0;pwned\x07name\x9b31m\x7f`; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTerminalBidiAndWhitespace(t *testing.T) {
	got := Terminal("select 1;\n\t-- \u202Eevil\u2066")
	if strings.ContainsAny(got, "\n\t\u202E\u2066") {
		t.Fatalf("got %q", got)
	}
	if want := "select 1;  -- \\u202eevil\\u2066"; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestTerminalKeepsPrintableUnicode(t *testing.T) {
	in := "naïve café — 日本語 — Ελληνικά"
	if got := Terminal(in); got != in {
		t.Errorf("got %q", got)
	}
}
