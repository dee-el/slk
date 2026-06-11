package messages

import (
	"strings"
	"testing"
)

func TestHighlightSearchTerms_PlainText(t *testing.T) {
	got := HighlightSearchTerms("deploy went fine", []string{"deploy"}, "\x1b[7m", "\x1b[27m")
	want := "\x1b[7mdeploy\x1b[27m went fine"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestHighlightSearchTerms_WordPrefixOnly(t *testing.T) {
	// "ploy" is not at a word start; must not highlight inside "deploy".
	got := HighlightSearchTerms("deploy", []string{"ploy"}, "[", "]")
	if got != "deploy" {
		t.Errorf("mid-word match highlighted: %q", got)
	}
	// but "dep" at word start highlights the prefix
	got = HighlightSearchTerms("deploy", []string{"dep"}, "[", "]")
	if got != "[dep]loy" {
		t.Errorf("prefix: %q", got)
	}
}

func TestHighlightSearchTerms_CaseAndAccentInsensitive(t *testing.T) {
	got := HighlightSearchTerms("Café open", []string{"cafe"}, "[", "]")
	if got != "[Café] open" {
		t.Errorf("fold: %q", got)
	}
}

func TestHighlightSearchTerms_SkipsANSISequences(t *testing.T) {
	in := "\x1b[31mdeploy\x1b[0m fine"
	got := HighlightSearchTerms(in, []string{"deploy"}, "[", "]")
	// The ANSI color sequence is preserved; visible text "deploy" is
	// wrapped; active sequences are re-applied after the highlight end.
	if !strings.Contains(got, "[deploy]") {
		t.Errorf("match not highlighted across ANSI: %q", got)
	}
	if !strings.Contains(got, "\x1b[31m") {
		t.Errorf("original ANSI dropped: %q", got)
	}
}

func TestHighlightSearchTerms_NoTerms(t *testing.T) {
	if got := HighlightSearchTerms("anything", nil, "[", "]"); got != "anything" {
		t.Errorf("no-op expected: %q", got)
	}
}

func TestHighlightSearchTerms_MultipleTermsAndOccurrences(t *testing.T) {
	got := HighlightSearchTerms("foo bar foo", []string{"foo", "bar"}, "[", "]")
	if got != "[foo] [bar] [foo]" {
		t.Errorf("got %q", got)
	}
}
