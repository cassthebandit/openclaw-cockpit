// File lifecycle.go carries the read-only, presentation-only detector for
// managed agent TUIs that have finished their work but idle at a prompt instead
// of exiting. It never writes tmux metadata, never kills, and never changes
// session hygiene — it only lets the UI reclassify a finished pane as
// "idle-finished" so it renders under Completed Agent Runs, not as live work.
package ui

import (
	"regexp"
	"strings"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

const (
	// idleFinishedTailLines bounds how much of the pane tail is inspected, for
	// performance and false-positive control (committee §9.3: ~last 50 lines).
	idleFinishedTailLines = 50
	// idleFinishedPromptWindow bounds how close to the bottom an idle prompt
	// line must appear to count as "waiting at a prompt".
	idleFinishedPromptWindow = 8
)

var (
	// ansiCSI matches CSI escape sequences (colours, cursor movement).
	ansiCSI = regexp.MustCompile("\x1b\\[[0-9;:?]*[ -/]*[@-~]")
	// ansiOSC matches OSC sequences terminated by BEL or ST.
	ansiOSC = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
)

// idleFinishedActiveMarkers veto an idle-finished verdict: if any appear in the
// bounded tail the agent is still working, so the pane stays live. Erring toward
// a longer list is the safe direction — a false veto keeps a finished pane
// visible, whereas a false positive would hide a working agent.
var idleFinishedActiveMarkers = []string{
	"esc to interrupt",
	"still running",
	"waiting for background terminal",
	"ctrl + t to view transcript",
	"ctrl+t to view transcript",
	"tool call in progress",
	"running tool",
	"executing command",
	"generating…",
	"generating...",
	"thinking…",
	"esc to cancel",
}

// idleFinishedCompletionMarkers are runtime-aware "the run finished" signatures.
// Deliberately NOT Codex-only: Codex says "worked for", Fable/Claude panes show
// "cooked for"/"brewed for", Gemini/AGY use "completed in"/"task complete", etc.
var idleFinishedCompletionMarkers = []string{
	"worked for ",
	"cooked for ",
	"brewed for ",
	"ran for ",
	"done in ",
	"completed in ",
	"finished in ",
	"goal achieved",
	"goal complete",
	"task complete",
	"all done",
}

// idleFinishedPromptPrefixes identify an idle input prompt after border glyphs
// are stripped (Codex/Claude/Fable `›`, some TUIs `❯`/`▌`, plain `>`).
var idleFinishedPromptPrefixes = []string{"›", "❯", "▌", "> ", ">"}

// idleFinishedReadyMarkers are alternate idle/ready signals for runtimes that do
// not render a leading prompt glyph (e.g. Gemini/AGY input hints).
var idleFinishedReadyMarkers = []string{
	"type your message",
	"waiting for your input",
	"ready for the next",
	"ready for input",
}

// paneIsManagedAgent gates idle-finished detection to managed agent panes only
// (contract via agent_wall, agent-ish kind or an agent label). Display-only
// adopted panes and services are excluded to avoid false positives.
func paneIsManagedAgent(pane tmux.Pane) bool {
	if pane.Cockpit == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(pane.Cockpit.ManagedBy), "agent_wall") {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(pane.Cockpit.Kind)) {
	case "agent", "visible-agent", "batch-worker", "smoke":
		return true
	case "service", "runtime":
		// A service/runtime card can carry an @oc_agent label but is not an
		// interactive agent run; never treat it as idle-finished.
		return false
	}
	return strings.TrimSpace(pane.Cockpit.Agent) != ""
}

// paneIdleFinished reports whether a managed agent pane has finished its work but
// is idling at a prompt. It is read-only: it inspects captured pane text and
// writes nothing. Detection requires a completion/summary marker AND an idle
// prompt/ready marker in a bounded tail, with active markers vetoing.
func paneIdleFinished(pane tmux.Pane) bool {
	if !paneIsManagedAgent(pane) {
		return false
	}
	return screenIsIdleFinished(pane.PreviewText)
}

// screenIsIdleFinished holds the pure text predicate so tests can exercise it on
// captured pane excerpts without constructing panes.
func screenIsIdleFinished(text string) bool {
	lines := nonEmptyLines(stripANSI(text))
	if len(lines) == 0 {
		return false
	}
	tail := lines
	if len(tail) > idleFinishedTailLines {
		tail = tail[len(tail)-idleFinishedTailLines:]
	}
	lowered := strings.ToLower(strings.Join(tail, "\n"))

	if containsAny(lowered, idleFinishedActiveMarkers...) {
		return false
	}
	if !hasIdlePromptMarker(tail, lowered) {
		return false
	}
	return containsAny(lowered, idleFinishedCompletionMarkers...)
}

func hasIdlePromptMarker(tail []string, lowered string) bool {
	window := tail
	if len(window) > idleFinishedPromptWindow {
		window = window[len(window)-idleFinishedPromptWindow:]
	}
	for _, line := range window {
		trimmed := strings.TrimSpace(stripPromptBorder(line))
		for _, marker := range idleFinishedPromptPrefixes {
			if strings.HasPrefix(trimmed, marker) {
				return true
			}
		}
	}
	return containsAny(lowered, idleFinishedReadyMarkers...)
}

// stripPromptBorder removes leading box-drawing glyphs and whitespace so a
// bordered input line like "│ › " is recognised as a "›" prompt.
func stripPromptBorder(line string) string {
	return strings.TrimLeft(line, " \t│|╭╰╮╯─┌└┐┘▏▕▎▐ ")
}

// stripANSI removes CSI/OSC escape sequences and stray C0 control characters
// (keeping newlines and tabs) so marker matching is not fooled by colour codes.
func stripANSI(s string) string {
	s = ansiCSI.ReplaceAllString(s, "")
	s = ansiOSC.ReplaceAllString(s, "")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' {
			b.WriteRune(r)
			continue
		}
		if r == 0x1b || r < 0x20 || r == 0x7f {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func nonEmptyLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		trimmed := strings.TrimRight(line, " \t\r")
		if strings.TrimSpace(trimmed) == "" {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}
