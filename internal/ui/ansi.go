package ui

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The color-preserving renderer still uses OSC-only removal.
var ansiOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")

// stripANSI preserves the classifier's historical CSI-then-OSC removal,
// followed by C0/DEL cleanup (except newline/tab) and UTF-8 replacement.
// CSI must be removed first: it can interrupt or even form an OSC sequence.
// This deliberately matches the old regex grammar, not all terminal escapes.
func stripANSI(s string) string {
	s = stripCSI(s)
	var b strings.Builder
	start := 0
	for i := 0; i < len(s); {
		end := i
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == ']' {
			j := i + 2
			for j < len(s) && s[j] != 0x07 && s[j] != 0x1b {
				j++
			}
			if j < len(s) && s[j] == 0x07 {
				end = j + 1
			} else if j+1 < len(s) && s[j] == 0x1b && s[j+1] == '\\' {
				end = j + 2
			}
		}
		if end == i && (s[i] < 0x20 && s[i] != '\n' && s[i] != '\t' || s[i] == 0x7f) {
			end = i + 1
		}
		if end > i {
			b.WriteString(s[start:i])
			i, start = end, end
			continue
		}
		if s[i] >= utf8.RuneSelf {
			r, size := utf8.DecodeRuneInString(s[i:])
			if r == utf8.RuneError && size == 1 {
				b.WriteString(s[start:i])
				b.WriteRune(utf8.RuneError)
				start = i + 1
			}
			i += size
		} else {
			i++
		}
	}
	if start == 0 {
		return s
	}
	b.WriteString(s[start:])
	return b.String()
}

// stripCSI matches ESC [ [0-9;:?]* [ -/]* [@-~], leftmost first.
func stripCSI(s string) string {
	var b strings.Builder
	start := 0
	for i := 0; i+1 < len(s); i++ {
		if s[i] != 0x1b || s[i+1] != '[' {
			continue
		}
		j := i + 2
		for j < len(s) && (s[j] >= '0' && s[j] <= '9' || s[j] == ';' || s[j] == ':' || s[j] == '?') {
			j++
		}
		for j < len(s) && s[j] >= ' ' && s[j] <= '/' {
			j++
		}
		if j < len(s) && s[j] >= '@' && s[j] <= '~' {
			if start == 0 {
				b.Grow(len(s))
			}
			b.WriteString(s[start:i])
			start = j + 1
			i = j
		}
	}
	if start == 0 {
		return s
	}
	b.WriteString(s[start:])
	return b.String()
}
