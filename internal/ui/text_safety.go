package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

// cardSafeLine is the single strict sanitizer for every untrusted string that
// reaches terminal chrome as one line: session/window/pane names and titles,
// detail/tab labels, pane-variable keys and values, tmux and runtime errors,
// janitor details, and external path/reason text. It removes whole terminal
// escape sequences (OSC including OSC 8/52, CSI, other ESC-introduced forms),
// C0/C1 controls, BEL and CR, collapses injected newlines/tabs into single
// spaces, and drops emoji and ambiguous/wide runes per the card-safety policy.
// Pane bodies deliberately do NOT go through this: they keep the existing
// normalized-SGR path (normalizeAgentCLIANSI / stripANSI).
func cardSafeLine(value string) string {
	return strings.Join(strings.Fields(cardSafeText(value)), " ")
}

// cardSafeBlock applies the strict sanitizer per line, preserving intentional
// line structure (multi-line Cockpit-composed preview text).
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
	value = stripEscapeSequences(value)
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		switch r {
		case '\t', '\r', '\n':
			// Injected line breaks and tabs collapse to a single space in
			// single-line chrome (cardSafeBlock splits on \n first, so real
			// multi-line content never reaches this case with \n).
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

// stripEscapeSequences removes complete ESC-introduced terminal sequences —
// CSI (with parameters and final byte), OSC (payload through BEL/ST, or to
// end-of-string when unterminated), DCS/SOS/PM/APC strings, and two-byte
// escapes — so no printable payload residue (for example an OSC 8 URL)
// survives as forged chrome text. Stray C0/C1 controls that remain are
// dropped by the per-rune filter in cardSafeText.
func stripEscapeSequences(value string) string {
	if !strings.ContainsRune(value, 0x1b) {
		return value
	}
	var b strings.Builder
	b.Grow(len(value))
	for i := 0; i < len(value); {
		if value[i] != 0x1b {
			b.WriteByte(value[i])
			i++
			continue
		}
		i++ // consume ESC
		if i >= len(value) {
			break
		}
		switch value[i] {
		case '[': // CSI: parameters/intermediates, then one final byte @-~
			i++
			for i < len(value) && (value[i] < '@' || value[i] > '~') {
				i++
			}
			if i < len(value) {
				i++
			}
		case ']', 'P', 'X', '^', '_': // OSC / DCS / SOS / PM / APC strings
			i++
			for i < len(value) {
				if value[i] == 0x07 { // BEL terminator
					i++
					break
				}
				if value[i] == 0x1b {
					if i+1 < len(value) && value[i+1] == '\\' { // ST terminator
						i += 2
					}
					// Bare ESC ends the string sequence; leave it for the
					// outer loop so a following sequence is still stripped.
					break
				}
				i++
			}
		default: // ESC c, ESC 7, charset selection (ESC ( B), ...
			for i < len(value) && value[i] >= 0x20 && value[i] <= 0x2f {
				i++ // intermediate bytes
			}
			if i < len(value) {
				i++ // final byte
			}
		}
	}
	return b.String()
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
	return normalizeANSIForCard(value, false)
}

func normalizeAgentCLIANSI(value string) string {
	return normalizeANSIForCard(value, true)
}

func normalizeANSIForCard(value string, readableDarkForeground bool) string {
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
				if sgr := normalizeSGRCardParams(value[i+2:end], readableDarkForeground); sgr != "" {
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
	return normalizeSGRCardParams(params, false)
}

func normalizeSGRCardParams(params string, readableDarkForeground bool) string {
	if params == "" {
		if readableDarkForeground {
			return "0;38;5;246"
		}
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
		case readableDarkForeground && n == 0:
			kept = append(kept, "0", "38", "5", "246")
		case readableDarkForeground && n == 39:
			kept = append(kept, "38", "5", "246")
		case readableDarkForeground && isDarkBasicForeground(n):
			kept = append(kept, "38", "5", "246")
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
		case n == 38:
			if readableDarkForeground && i+2 < len(parts) && parts[i+1] == "5" {
				color, ok := parseDecimalParam(parts[i+2])
				if ok && isDarkExtendedForeground(color) {
					kept = append(kept, "38", "5", "246")
					i += 2
					continue
				}
			}
			if readableDarkForeground && i+4 < len(parts) && parts[i+1] == "2" {
				r, okR := parseDecimalParam(parts[i+2])
				g, okG := parseDecimalParam(parts[i+3])
				b, okB := parseDecimalParam(parts[i+4])
				if okR && okG && okB && r <= 64 && g <= 64 && b <= 64 {
					kept = append(kept, "38", "5", "246")
					i += 4
					continue
				}
			}
			kept = append(kept, part)
		default:
			kept = append(kept, part)
		}
	}
	if len(kept) == 0 {
		return ""
	}
	return strings.Join(kept, ";")
}

func isDarkBasicForeground(n int) bool {
	return n == 30 || n == 90
}

func isDarkExtendedForeground(n int) bool {
	return n == 0 || n == 8 || (n >= 16 && n <= 19) || (n >= 232 && n <= 236)
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
