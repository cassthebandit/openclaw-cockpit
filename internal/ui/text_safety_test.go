package ui

import (
	"errors"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"
)

// hostileChrome bundles the adversarial control-sequence payloads from AC3:
// OSC 52 clipboard write, OSC 8 hyperlink, cursor-moving CSI, BEL, C1 CSI,
// CR, newline, and tab, each wrapped around identifiable markers.
const (
	hostileOSC52 = "\x1b]52;c;ZXZpbA==\x07clip"
	hostileOSC8  = "\x1b]8;;http://evil.example\x1b\\link-text\x1b]8;;\x1b\\"
	hostileCSI   = "\x1b[2Aup\x1b[10;10Hjump"
	// hostileC1 leads with U+009B, the C1 CSI control (UTF-8 bytes c2 9b).
	// Written as an escape rather than the raw rune so the adversarial
	// payload stays visible in editors and diffs; compiled bytes are unchanged.
	hostileC1 = "\u009b31mred"
	// hostileBareC1 leads with the bare 0x9b byte - the 8-bit C1 CSI form,
	// which is invalid UTF-8 rather than an encoded U+009B. It pins that a
	// byte-oriented sanitizer rewrite cannot let the raw control byte through.
	hostileBareC1  = "\x9b31mred"
	hostileNewline = "line1\nFORGEDLINE"
	hostileTabCR   = "a\tb\rc"
	hostileBEL     = "ding\x07dong"
)

func TestCardSafeLineStripsControlSequencesWholesale(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    string
		want  string
		avoid []string
	}{
		{"osc52", hostileOSC52, "clip", []string{"52;c", "ZXZpbA"}},
		{"osc8", hostileOSC8, "link-text", []string{"http://evil.example", "8;;"}},
		{"cursor-csi", hostileCSI, "upjump", []string{"[2A", "10;10"}},
		{"c1-csi", hostileC1, "31mred", []string{"\u009b"}},
		{"bare-c1-byte", hostileBareC1, "\uFFFD31mred", []string{"\x9b"}},
		{"newline", hostileNewline, "line1 FORGEDLINE", []string{"\n"}},
		{"tab-cr", hostileTabCR, "a b c", []string{"\t", "\r"}},
		{"bel", hostileBEL, "dingdong", []string{"\x07"}},
		{"unterminated-osc", "\x1b]52;c;steal", "", []string{"52;c", "steal"}},
		{"charset", "\x1b(Bok", "ok", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := cardSafeLine(tc.in)
			if got != tc.want {
				t.Fatalf("cardSafeLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			for _, bad := range tc.avoid {
				if strings.Contains(got, bad) {
					t.Fatalf("cardSafeLine(%q) kept forbidden fragment %q in %q", tc.in, bad, got)
				}
			}
		})
	}
}

// assertChromeSafe fails when rendered chrome contains any escape sequence
// other than plain SGR (ESC [ ... m), or any control rune besides newline.
// Lip Gloss styling legitimately emits SGR; everything else is an injection.
func assertChromeSafe(t *testing.T, name, rendered string) {
	t.Helper()
	for i := 0; i < len(rendered); {
		r, size := utf8.DecodeRuneInString(rendered[i:])
		if r == 0x1b {
			rest := rendered[i+1:]
			if !strings.HasPrefix(rest, "[") {
				t.Fatalf("%s: non-CSI escape at byte %d: %q", name, i, clip(rendered, i))
			}
			j := 1
			for j < len(rest) && (rest[j] == ';' || rest[j] == ':' || (rest[j] >= '0' && rest[j] <= '9')) {
				j++
			}
			if j >= len(rest) || rest[j] != 'm' {
				t.Fatalf("%s: non-SGR escape sequence at byte %d: %q", name, i, clip(rendered, i))
			}
			i += 1 + j + 1
			continue
		}
		if r != '\n' && unicode.IsControl(r) {
			t.Fatalf("%s: control rune %U at byte %d: %q", name, r, i, clip(rendered, i))
		}
		i += size
	}
}

func clip(s string, i int) string {
	end := i + 24
	if end > len(s) {
		end = len(s)
	}
	return s[i:end]
}

// hostileWallModel builds a model whose external inputs (session/window/pane
// names and titles, pane variables, tmux error, janitor detail) all carry
// terminal-control payloads.
func hostileWallModel(t *testing.T) *Model {
	t.Helper()
	name := "evil" + hostileOSC52 + hostileNewline
	session := tmux.Session{
		ID:           "$hostile",
		Name:         name,
		CreatedAt:    time.Unix(1752000000, 0),
		LastActivity: time.Unix(1752000000, 0),
		Windows: []tmux.Window{{
			ID:      "@hostile",
			Name:    "win" + hostileCSI,
			Active:  true,
			Session: "$hostile",
			Panes: []tmux.Pane{{
				ID:         "%hostile",
				Active:     true,
				Title:      "title" + hostileOSC8 + hostileC1,
				CurrentCmd: "cmd" + hostileBEL,
				CreatedAt:  time.Unix(1752000000, 0),
			}},
		}},
	}
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 120
	m.height = 40
	m.sessions = []tmux.Session{session}
	vp := viewportFor(innerDimension{width: 48, height: 8})
	m.previews[session.ID] = &sessionPreview{
		viewport:   &vp,
		paneID:     "%hostile",
		autoFollow: true,
		vars: map[string]string{
			"@oc_goal" + hostileCSI: "value" + hostileOSC52 + hostileNewline,
		},
	}
	m.err = errors.New("tmux failed: " + hostileOSC8 + hostileTabCR)
	m.janitorStatus = janitorStatusView{State: "invalid", Detail: "bad json " + hostileCSI}
	m.refreshLifecycleVerdicts()
	m.updateStaleSessions()
	return m
}

func TestHostileChromeNeverReachesTerminal(t *testing.T) {
	t.Parallel()

	m := hostileWallModel(t)

	// zone.Scan strips the internal zone-marker sequences exactly as View()
	// does before the frame reaches the terminal.
	scan := func(s string) string { return zone.Scan(s) }

	// Overview wall (cards, headers, dividers).
	assertChromeSafe(t, "overview", scan(m.renderSessionPreviews(0)))
	// Title bar and tab strip.
	assertChromeSafe(t, "titlebar", scan(renderTitleBar(m, 120)))
	assertChromeSafe(t, "tabs", scan(m.renderTabBar(120)))
	// Footer: helper, janitor line, error line, pane vars, toast.
	m.showToast("saw " + hostileOSC52)
	assertChromeSafe(t, "footer", scan(m.buildStatusLine(120)))

	// Detail view for the hostile session.
	m.enterDetail("$hostile")
	assertChromeSafe(t, "detail-title", scan(renderTitleBar(m, 120)))
	assertChromeSafe(t, "detail-tabs", scan(m.renderTabBar(120)))
	assertChromeSafe(t, "detail-footer", scan(m.buildStatusLine(120)))
	assertChromeSafe(t, "detail-cards", scan(m.renderSessionPreviews(0)))

	// Injected newlines must not fabricate an extra chrome line.
	for name, rendered := range map[string]string{
		"titlebar": scan(renderTitleBar(m, 120)),
		"tabs":     scan(m.renderTabBar(120)),
	} {
		if strings.Contains(rendered, "\nFORGEDLINE") {
			t.Fatalf("%s: injected newline forged a chrome line", name)
		}
	}
}

// TestPaneBodySGRPassthroughUnchanged pins the pane-body exception: the
// normalized SGR subset survives normalizeAgentCLIANSI while OSC and
// non-SGR CSI are still removed. The strict chrome sanitizer must not be
// applied to this path.
func TestPaneBodySGRPassthroughUnchanged(t *testing.T) {
	t.Parallel()

	in := "\x1b[38;5;114mgreen\x1b[0m " + hostileOSC52 + " \x1b[2Aup"
	got := normalizeAgentCLIANSI(in)
	if !strings.Contains(got, "\x1b[38;5;114m") {
		t.Fatalf("pane body must preserve accepted SGR, got %q", got)
	}
	if strings.Contains(got, "]52") || strings.Contains(got, "\x1b[2A") {
		t.Fatalf("pane body must still drop OSC/non-SGR CSI, got %q", got)
	}
}
