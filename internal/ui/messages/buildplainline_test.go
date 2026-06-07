package messages

import (
	"testing"

	"github.com/rivo/uniseg"
)

// buildPlainLineUniseg is the previous uniseg.NewGraphemes-based reference
// implementation, kept here only to prove the faster grapheme-only
// FirstGraphemeClusterInString rewrite in buildPlainLine produces an
// identical column->byte map.
func buildPlainLineUniseg(line string) plainLine {
	if line == "" {
		return plainLine{Text: "", Bytes: []int{0}}
	}
	g := uniseg.NewGraphemes(line)
	bytesMap := make([]int, 0, len(line))
	byteOffset := 0
	for g.Next() {
		cluster := g.Str()
		w := g.Width()
		if w <= 0 {
			byteOffset += len(cluster)
			continue
		}
		for k := 0; k < w; k++ {
			bytesMap = append(bytesMap, byteOffset)
		}
		byteOffset += len(cluster)
	}
	bytesMap = append(bytesMap, len(line))
	return plainLine{Text: line, Bytes: bytesMap}
}

func TestBuildPlainLine_MatchesUnisegGraphemes(t *testing.T) {
	cases := []string{
		"",
		"hello world",
		"a moderately long message with **bold** and _italic_ formatting.",
		"emoji 😀 here",
		"flag 🇺🇸 and family 👨‍👩‍👧‍👦 done",
		"wide 你好世界 chars",
		"combining e\u0301 acute",
		"zwj 👩‍💻 dev",
		"tabs\tand   spaces",
		"❤️ variation selector",
		"mixed 12 ab 漢字 😀 e\u0301 end",
		"trailing combining a\u0301",
	}
	for _, s := range cases {
		want := buildPlainLineUniseg(s)
		got := buildPlainLine(s)
		if got.Text != want.Text {
			t.Fatalf("Text mismatch for %q: got %q want %q", s, got.Text, want.Text)
		}
		if len(got.Bytes) != len(want.Bytes) {
			t.Fatalf("Bytes len mismatch for %q: got %v want %v", s, got.Bytes, want.Bytes)
		}
		for i := range want.Bytes {
			if got.Bytes[i] != want.Bytes[i] {
				t.Fatalf("Bytes[%d] mismatch for %q: got %v want %v", i, s, got.Bytes, want.Bytes)
			}
		}
	}
}
