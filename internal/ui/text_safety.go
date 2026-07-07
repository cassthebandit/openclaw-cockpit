package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func cardSafeLine(value string) string {
	return strings.Join(strings.Fields(cardSafeText(value)), " ")
}

func cardSafeBlock(value string) string {
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		lines[i] = cardSafeText(line)
	}
	return strings.Join(lines, "\n")
}

func cardSafeText(value string) string {
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		switch r {
		case '\t', '\r':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		if unicode.IsControl(r) || isEmojiRune(r) || runewidth.RuneWidth(r) != 1 {
			continue
		}
		b.WriteRune(r)
		lastSpace = unicode.IsSpace(r)
	}
	return strings.TrimSpace(b.String())
}

func isEmojiRune(r rune) bool {
	switch {
	case r >= 0x1F000 && r <= 0x1FAFF:
		return true
	case r >= 0x2600 && r <= 0x27BF:
		return true
	case r >= 0xFE00 && r <= 0xFE0F:
		return true
	case r == 0x200D:
		return true
	default:
		return false
	}
}

func stripANSIBackgrounds(value string) string {
	if value == "" {
		return ""
	}
	value = ansiOSC.ReplaceAllString(value, "")
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); {
		if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '[' {
			end := i + 2
			for end < len(value) {
				c := value[end]
				if c >= '@' && c <= '~' {
					break
				}
				end++
			}
			if end >= len(value) {
				break
			}
			if value[end] == 'm' {
				if sgr := stripSGRBackgroundParams(value[i+2 : end]); sgr != "" {
					b.WriteString("\x1b[")
					b.WriteString(sgr)
					b.WriteByte('m')
				}
			}
			i = end + 1
			continue
		}
		r, width := utf8.DecodeRuneInString(value[i:])
		if r == utf8.RuneError && width == 0 {
			break
		}
		i += width
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r == 0x1b || unicode.IsControl(r) {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func stripSGRBackgroundParams(params string) string {
	if params == "" {
		return "0"
	}
	parts := strings.Split(params, ";")
	kept := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		part := parts[i]
		if part == "" {
			part = "0"
		}
		n, ok := parseDecimalParam(part)
		if !ok {
			kept = append(kept, part)
			continue
		}
		switch {
		case n == 49 || (n >= 40 && n <= 47) || (n >= 100 && n <= 107):
			continue
		case n == 48:
			if i+1 < len(parts) {
				mode := parts[i+1]
				switch mode {
				case "5":
					i += min(2, len(parts)-1-i)
				case "2":
					i += min(5, len(parts)-1-i)
				}
			}
			continue
		default:
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, ";")
}

func parseDecimalParam(value string) (int, bool) {
	if value == "" {
		return 0, false
	}
	n := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	return n, true
}
