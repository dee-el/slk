package emoji

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type urlFixture struct {
	Base    string            `json:"base"`
	Entries []urlFixtureEntry `json:"entries"`
}

type urlFixtureEntry struct {
	Name       string `json:"name"`
	Codepoints []rune `json:"codepoints"`
	URL        string `json:"url"`
}

func loadURLFixture(t *testing.T) urlFixture {
	t.Helper()
	path := filepath.Join("testdata", "slack_urls.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var f urlFixture
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if len(f.Entries) == 0 {
		t.Fatalf("fixture has no entries")
	}
	return f
}

func TestBuildStandardEmojiURL(t *testing.T) {
	fixture := loadURLFixture(t)
	for _, e := range fixture.Entries {
		got := BuildStandardEmojiURL(e.Codepoints)
		if got != e.URL {
			t.Errorf("BuildStandardEmojiURL(%q codepoints=%v) = %q, want %q",
				e.Name, e.Codepoints, got, e.URL)
		}
	}
}

func TestCodepointsForShortcode_Builtin(t *testing.T) {
	cases := []struct {
		name string
		want []rune // expected codepoints
	}{
		{"thumbsup", []rune{0x1F44D}},
		{"heart", []rune{0x2764, 0xFE0F}},
		{"man_astronaut", []rune{0x1F468, 0x200D, 0x1F680}},
		{"warning", []rune{0x26A0, 0xFE0F}},
		{"fire", []rune{0x1F525}},
	}
	for _, c := range cases {
		got, ok := CodepointsForShortcode(c.name)
		if !ok {
			t.Errorf("CodepointsForShortcode(%q): ok=false, want a kyokomi hit", c.name)
			continue
		}
		if !runesEqual(got, c.want) {
			t.Errorf("CodepointsForShortcode(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCodepointsForShortcode_Unknown(t *testing.T) {
	if _, ok := CodepointsForShortcode("definitely_not_an_emoji_name_xyz"); ok {
		t.Errorf("CodepointsForShortcode(unknown): ok=true, want false")
	}
}

func runesEqual(a, b []rune) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
