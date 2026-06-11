package messages

import (
	"strings"
	"unicode"

	"github.com/gammons/slk/internal/text"
)

// HighlightSearchTerms wraps case- and accent-insensitive word-prefix
// occurrences of terms in s with hlStart/hlEnd. s may contain ANSI SGR
// escape sequences: they are skipped during matching, preserved in the
// output, and any sequences active at a match start are re-emitted
// after hlEnd so the highlight does not clobber surrounding styling.
//
// terms must already be folded (text.Fold). Matching is per-rune
// folded comparison, which keeps a 1:1 position mapping for the
// diacritics Fold removes. Matches spanning a styled-segment boundary
// highlight only up to the boundary — acceptable for v1.
func HighlightSearchTerms(s string, terms []string, hlStart, hlEnd string) string {
	if len(terms) == 0 || s == "" {
		return s
	}

	type seg struct {
		text   string // visible run or escape sequence
		isANSI bool
	}
	var segs []seg
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) {
				j++ // include final byte
			}
			segs = append(segs, seg{text: s[i:j], isANSI: true})
			i = j
			continue
		}
		j := strings.IndexByte(s[i:], 0x1b)
		if j < 0 {
			j = len(s) - i
		}
		segs = append(segs, seg{text: s[i : i+j]})
		i += j
	}

	var out strings.Builder
	var active []string // SGR sequences since last reset, for re-apply
	prevRune := rune(0) // last visible rune across segments (word boundary)
	for _, sg := range segs {
		if sg.isANSI {
			out.WriteString(sg.text)
			if sg.text == "\x1b[0m" || sg.text == "\x1b[m" {
				active = active[:0]
			} else {
				active = append(active, sg.text)
			}
			continue
		}
		runes := []rune(sg.text)
		folded := make([]string, len(runes))
		for i, r := range runes {
			folded[i] = text.Fold(string(r))
		}
		for i := 0; i < len(runes); {
			atWordStart := !unicode.IsLetter(prevRune) && !unicode.IsDigit(prevRune)
			matched := 0
			if atWordStart {
				for _, term := range terms {
					if n := prefixMatchLen(folded, i, term); n > 0 {
						matched = n
						break
					}
				}
			}
			if matched > 0 {
				out.WriteString(hlStart)
				out.WriteString(string(runes[i : i+matched]))
				out.WriteString(hlEnd)
				for _, a := range active {
					out.WriteString(a)
				}
				prevRune = runes[i+matched-1]
				i += matched
				continue
			}
			out.WriteRune(runes[i])
			prevRune = runes[i]
			i++
		}
	}
	return out.String()
}

// prefixMatchLen reports how many runes starting at folded[i] are
// consumed matching term, or 0 if term is not a prefix there.
func prefixMatchLen(folded []string, i int, term string) int {
	rest := term
	n := 0
	for i+n < len(folded) && rest != "" {
		f := folded[i+n]
		if !strings.HasPrefix(rest, f) {
			return 0
		}
		rest = rest[len(f):]
		n++
	}
	if rest != "" {
		return 0
	}
	return n
}
