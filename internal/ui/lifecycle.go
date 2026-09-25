// File lifecycle.go carries the read-only, presentation-only classifier for
// agent TUIs. It never writes tmux metadata, never kills, and never changes
// session hygiene — it only lets the UI stop presenting finished prompt-idle
// panes as live work.
package ui

import (
	"regexp"
	"strings"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

const (
	// idleFinishedTailLines bounds how much of the pane tail is inspected, for
	// performance and false-positive control (committee §9.3: ~last 50 lines).
	idleFinishedTailLines = 50
	// idleFinishedPromptWindow bounds how close to the bottom an idle prompt
	// line must appear to count as "waiting at a prompt".
	idleFinishedPromptWindow = 8
)

type paneLifecycleVerdict struct {
	state      string
	confidence int
	reasons    []string
}

var (
	failedWord              = regexp.MustCompile(`(?m)\bFAILED\b`)
	durationCompletionShape = regexp.MustCompile(`(?i)\b[\pL]+(?:ed|n)\s+for\s+\d+\s*(?:s|m|h|d|sec|secs|second|seconds|min|mins|minute|minutes|hr|hrs|hour|hours)\b`)
	numberedChoiceShape     = regexp.MustCompile(`(?m)^\s*(?:[1-9][0-9]*[\.)]\s+|[❯>]\s*[1-9][0-9]*\b)`)
	operatorDecisionShape   = regexp.MustCompile(`(?m)\b[1-9][0-9]*\.\s*(approve|reject|deny|edit|allow)\b`)
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

var lifecycleOperatorMarkers = []string{
	"approve plan",
	"approval required",
	"apply changes?",
	"proceed?",
	"permission requested",
	"allow command",
	"allow this command",
	"device code",
	"authorize",
	"log in to github",
	"ready to code?",
	"would you like to proceed?",
}

var lifecycleErrorMarkers = []string{
	"error:",
	"permission denied",
	"unauthorized",
	"rate limit",
	"auth required",
	"panic:",
	"traceback",
	"failed:",
	" failed ",
	" failed\n",
	"\nfailed ",
}

// idleFinishedCompletionMarkers are runtime-aware "the run finished" signatures.
// Deliberately NOT Codex-only: Codex says "worked for", Fable/Claude panes show
// "cooked for"/"brewed for"/"baked for", Gemini/AGY use "completed in"/
// "task complete", etc.
var idleFinishedCompletionMarkers = []string{
	"worked for ",
	"cooked for ",
	"brewed for ",
	"baked for ",
	"sautéed for ",
	"sauteed for ",
	"crunched for ",
	"ran for ",
	"done in ",
	"completed in ",
	"finished in ",
	"goal achieved",
	"goal complete",
	"task complete",
	"all done",
	"ready_for_parent_review",
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
	case "service", "viewer", "runtime":
		// A service/runtime card can carry an @oc_agent label but is not an
		// interactive agent run; never treat it as idle-finished.
		return false
	}
	return strings.TrimSpace(pane.Cockpit.Agent) != ""
}

func paneHasAgentIdentity(pane tmux.Pane) bool {
	if pane.Cockpit == nil {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(pane.Cockpit.Kind))
	if kind == "service" || kind == "runtime" {
		return false
	}
	if strings.TrimSpace(pane.Cockpit.Agent) != "" {
		return true
	}
	switch kind {
	case "agent", "visible-agent", "batch-worker", "smoke":
		return true
	default:
		return false
	}
}

// lifecycleInput snapshots the classifier inputs by value. In particular, a
// Cockpit pointer can be reused and mutated by a runtime merge.
// Keep this in sync with paneLifecycleVerdictFor and its metadata predicates.
type lifecycleInput struct {
	content                                      string
	agentLike, agentIdentity, dead, liveMetadata bool
	deadStatus                                   int
	state                                        string
}

func lifecycleInputFor(pane tmux.Pane, agentLike bool) lifecycleInput {
	input := lifecycleInput{
		content: pane.PreviewText, agentLike: agentLike,
		agentIdentity: paneHasAgentIdentity(pane), dead: pane.Dead,
		deadStatus: pane.DeadStatus, liveMetadata: liveishMetadata(pane),
	}
	if pane.Cockpit != nil {
		input.state = pane.Cockpit.State
	}
	return input
}

func (m *Model) updateLifecycleVerdict(pane tmux.Pane, agentLike bool) paneLifecycleVerdict {
	input := lifecycleInputFor(pane, agentLike)
	if previous, ok := m.lifecycleInputs[pane.ID]; ok && previous == input {
		if verdict, exists := m.lifecycleVerdicts[pane.ID]; exists {
			return verdict
		}
	}
	if m.lifecycleInputs == nil {
		m.lifecycleInputs = make(map[string]lifecycleInput)
	}
	if m.lifecycleVerdicts == nil {
		m.lifecycleVerdicts = make(map[string]paneLifecycleVerdict)
	}
	verdict := paneLifecycleVerdictFor(pane, agentLike)
	m.lifecycleInputs[pane.ID] = input
	m.lifecycleVerdicts[pane.ID] = verdict
	return verdict
}

// refreshLifecycleVerdicts refreshes every pane's verdict from the freshest
// content available: the snapshot's PreviewText, or the captured preview
// content when the snapshot carries none. Verdicts for panes whose content
// arrives later via capture are updated per pane in refreshPaneLifecycleVerdict,
// which is what keeps cachedLifecycleVerdict a pure lookup on the hot path.
func (m *Model) refreshLifecycleVerdicts() {
	if m == nil {
		return
	}
	seen := make(map[string]struct{}, len(m.lifecycleInputs))
	for _, session := range m.sessions {
		agentLike := m.sessionIsAgentLike(session)
		for _, window := range session.Windows {
			for _, pane := range window.Panes {
				if strings.TrimSpace(pane.PreviewText) == "" {
					if preview := m.previews[session.ID]; preview != nil && preview.paneID == pane.ID {
						pane.PreviewText = preview.lastContent
					}
				}
				m.updateLifecycleVerdict(pane, agentLike)
				seen[pane.ID] = struct{}{}
			}
		}
	}
	for id := range m.lifecycleInputs {
		if _, ok := seen[id]; !ok {
			delete(m.lifecycleInputs, id)
			delete(m.lifecycleVerdicts, id)
		}
	}
}

// refreshPaneLifecycleVerdict recomputes a single pane's cached verdict after
// its captured content changed (paneContentMsg), so classification stays fresh
// between snapshots without re-running ANSI stripping on every read.
func (m *Model) refreshPaneLifecycleVerdict(sessionID, paneID, content string) {
	if m == nil {
		return
	}
	session, ok := m.sessionByID(sessionID)
	if !ok {
		return
	}
	agentLike := m.sessionIsAgentLike(session)
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.ID != paneID {
				continue
			}
			if strings.TrimSpace(pane.PreviewText) == "" {
				pane.PreviewText = content
			}
			m.updateLifecycleVerdict(pane, agentLike)
			return
		}
	}
}

// cachedLifecycleVerdict returns the pane's cached verdict. The cache is kept
// fresh by refreshLifecycleVerdicts (per snapshot) and
// refreshPaneLifecycleVerdict (per capture), so hot paths never re-run the
// ANSI-stripping classifier; the compute fallback only covers panes that have
// not been through either refresh (e.g. models built directly in tests).
func (m *Model) cachedLifecycleVerdict(pane tmux.Pane, session tmux.Session) paneLifecycleVerdict {
	if m != nil && len(m.lifecycleVerdicts) > 0 {
		if verdict, ok := m.lifecycleVerdicts[pane.ID]; ok {
			return verdict
		}
	}
	if strings.TrimSpace(pane.PreviewText) == "" {
		if m != nil {
			if preview := m.previews[session.ID]; preview != nil && preview.paneID == pane.ID {
				pane.PreviewText = preview.lastContent
			}
		}
	}
	return paneLifecycleVerdictFor(pane, m.sessionIsAgentLike(session))
}

func paneLifecycleVerdictFor(pane tmux.Pane, agentLike bool) paneLifecycleVerdict {
	if !agentLike && !paneHasAgentIdentity(pane) {
		return paneLifecycleVerdict{state: "unknown", confidence: 0}
	}
	if pane.Dead {
		if pane.DeadStatus != 0 {
			return paneLifecycleVerdict{state: "terminal-problem", confidence: 100, reasons: []string{"pane-dead-nonzero"}}
		}
		if liveishMetadata(pane) {
			return paneLifecycleVerdict{state: "stale", confidence: 95, reasons: []string{"dead-live-metadata"}}
		}
		return paneLifecycleVerdict{state: "terminal-done", confidence: 100, reasons: []string{"pane-dead-zero"}}
	}

	lines := nonEmptyLines(stripANSI(pane.PreviewText))
	if len(lines) == 0 {
		return paneLifecycleVerdict{state: "unknown", confidence: 0}
	}
	tail := lines
	if len(tail) > idleFinishedTailLines {
		tail = tail[len(tail)-idleFinishedTailLines:]
	}
	lowered := strings.ToLower(strings.Join(tail, "\n"))

	if containsAny(lowered, idleFinishedActiveMarkers...) {
		return paneLifecycleVerdict{state: "live-working", confidence: 85, reasons: []string{"active-marker"}}
	}
	operatorTail := tail
	if len(operatorTail) > 12 {
		operatorTail = operatorTail[len(operatorTail)-12:]
	}
	if hasOperatorPrompt(operatorTail) {
		return paneLifecycleVerdict{state: "awaiting-operator", confidence: 80, reasons: []string{"operator-marker"}}
	}
	if hasIdlePromptMarkerFor(tail, lowered, paneHasAgentIdentity(pane)) && hasCompletionMarkerFor(lowered, paneHasAgentIdentity(pane)) {
		return paneLifecycleVerdict{state: "delivered-idle", confidence: 85, reasons: []string{"completion-marker", "prompt-marker"}}
	}
	if hasErrorMarker(lowered) {
		return paneLifecycleVerdict{state: "failed", confidence: 70, reasons: []string{"error-marker"}}
	}

	if pane.Cockpit != nil {
		switch strings.ToLower(strings.TrimSpace(pane.Cockpit.State)) {
		case "done", "pass", "signal", "directional", "null-safe", "held":
			return paneLifecycleVerdict{state: "terminal-done", confidence: 80, reasons: []string{"metadata-terminal"}}
		case "failed", "route-fail", "safety-fail":
			return paneLifecycleVerdict{state: "terminal-problem", confidence: 80, reasons: []string{"metadata-problem"}}
		case "stale":
			return paneLifecycleVerdict{state: "stale", confidence: 80, reasons: []string{"metadata-stale"}}
		case "starting", "running", "waiting", "blocked", "review":
			return paneLifecycleVerdict{state: "live-working", confidence: 25, reasons: []string{"metadata-live"}}
		}
	}
	return paneLifecycleVerdict{state: "unknown", confidence: 0}
}

func liveishMetadata(pane tmux.Pane) bool {
	if pane.Cockpit == nil {
		return false
	}
	if strings.TrimSpace(pane.Cockpit.CompletedAt) != "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(pane.Cockpit.State)) {
	case "running", "starting", "waiting", "blocked":
		return true
	default:
		return false
	}
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
	return hasCompletionMarkerFor(lowered, true)
}

func hasIdlePromptMarker(tail []string, lowered string) bool {
	return hasIdlePromptMarkerFor(tail, lowered, true)
}

func hasIdlePromptMarkerFor(tail []string, lowered string, allowBarePrompt bool) bool {
	window := tail
	if len(window) > idleFinishedPromptWindow {
		window = window[len(window)-idleFinishedPromptWindow:]
	}
	for _, line := range window {
		trimmed := strings.TrimSpace(stripPromptBorder(line))
		for _, marker := range idleFinishedPromptPrefixes {
			if !allowBarePrompt && (marker == ">" || marker == "> ") {
				continue
			}
			if strings.HasPrefix(trimmed, marker) {
				return true
			}
		}
	}
	return containsAny(lowered, idleFinishedReadyMarkers...)
}

func hasCompletionMarkerFor(lowered string, managed bool) bool {
	if managed {
		return containsAny(lowered, idleFinishedCompletionMarkers...) || durationCompletionShape.MatchString(lowered)
	}
	for _, marker := range []string{
		"worked for ",
		"cooked for ",
		"brewed for ",
		"baked for ",
		"sautéed for ",
		"sauteed for ",
		"crunched for ",
		"goal achieved",
		"goal complete",
		"task complete",
		"completed in ",
		"finished in ",
		"ready_for_parent_review",
	} {
		if strings.Contains(lowered, marker) {
			return true
		}
	}
	return false
}

func hasOperatorPrompt(tail []string) bool {
	lowered := strings.ToLower(strings.Join(tail, "\n"))
	if lowered == "" {
		return false
	}
	if containsAny(lowered, lifecycleOperatorMarkers...) {
		return true
	}
	menuPhrases := []string{"choose an option", "select an option", "what would you like to do", "press 1"}
	if containsAny(lowered, menuPhrases...) && numberedChoiceShape.MatchString(lowered) {
		return true
	}
	return operatorDecisionShape.MatchString(lowered)
}

func hasErrorMarker(lowered string) bool {
	if strings.Contains(lowered, "0 failed") || strings.Contains(lowered, "no failed") {
		return false
	}
	if containsAny(lowered, lifecycleErrorMarkers...) {
		return true
	}
	return failedWord.MatchString(strings.ToUpper(lowered))
}

// stripPromptBorder removes leading box-drawing glyphs and whitespace so a
// bordered input line like "│ › " is recognised as a "›" prompt.
func stripPromptBorder(line string) string {
	return strings.TrimLeft(line, " \t│|╭╰╮╯─┌└┐┘▏▕▎▐")
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

// Kind vocabulary is shared; ownership and service exclusions remain distinct.
func isAgentKind(kind string) bool {
	switch kind {
	case "agent", "visible-agent", "batch-worker", "smoke":
		return true
	}
	return false
}

func (m *Model) sessionIsAgentLike(session tmux.Session) bool {
	return sessionHasManagedAgent(session) || containsKeywords(sessionChromeText(session), m.groupingConfig().AgentKeywords)
}
