package ui

import (
	"strings"
	"unicode"

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
