package ui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// agentNameTokens identify an agent/review session by its chrome (name, window,
// title, command) when it carries no managed cockpit metadata.
var agentNameTokens = []string{
	"committee", "fable", "codex", "claude", "gemini",
	"agy", "antigravity", "opencode", "aider",
}

// sessionClassification memoizes the two per-session classifiers that
// dominate frame cost (group routing and attention state). Entries are only
// valid until invalidateClassifications, which runs whenever classification
// inputs change: snapshot swap, pane capture content, stale-set recompute.
type sessionClassification struct {
	groupKnown     bool
	group          cockpitGroup
	attentionKnown bool
	attention      string
}

func (m *Model) classifyEntry(sessionID string) *sessionClassification {
	if m == nil || sessionID == "" {
		return nil
	}
	if m.classifyCache == nil {
		m.classifyCache = make(map[string]*sessionClassification)
	}
	entry, ok := m.classifyCache[sessionID]
	if !ok {
		entry = &sessionClassification{}
		m.classifyCache[sessionID] = entry
	}
	return entry
}

// invalidateClassifications drops all memoized group/attention results. Call
// after any cross-session change to m.sessions, m.lifecycleVerdicts,
// m.artifactOutcomes, or m.stale — the inputs the classifiers read.
// Classification changes can move cards between groups, reorder the wall, and
// change attention labels, so invalidation always dirties the frame cache:
// F6/F7 invalidation and F1 dirtiness are one accounting system.
func (m *Model) invalidateClassifications() {
	if m == nil {
		return
	}
	m.markRenderDirty()
	if len(m.classifyCache) == 0 {
		return
	}
	clear(m.classifyCache)
}

// invalidateClassification drops one session's memoized group/attention
// result after a change scoped to that session's own inputs (its captured
// pane content and per-pane lifecycle verdict). Changes to cross-session
// inputs — snapshot swaps, the stale set, artifact outcomes — must use
// invalidateClassifications instead. Like the global form, it always dirties
// the frame cache so F6 invalidation and F1 dirtiness cannot disagree.
func (m *Model) invalidateClassification(sessionID string) {
	if m == nil || sessionID == "" {
		return
	}
	m.markRenderDirty()
	if len(m.classifyCache) == 0 {
		return
	}
	delete(m.classifyCache, sessionID)
}

func cockpitGroupFor(m *Model, session tmux.Session) cockpitGroup {
	entry := m.classifyEntry(session.ID)
	if entry != nil && entry.groupKnown {
		return entry.group
	}
	group := computeCockpitGroupFor(m, session)
	if entry != nil {
		entry.group = group
		entry.groupKnown = true
	}
	return group
}

func computeCockpitGroupFor(m *Model, session tmux.Session) cockpitGroup {
	// 1. OpenClaw runtime snapshot cards route by presentationGroup first.
	if group, ok := openClawRuntimeGroupFor(session); ok {
		return group
	}

	state := sessionAttentionState(m, session)

	// 2. Managed agent identity is classified BEFORE the attention override and
	//    before dashboard/viewer/service keyword heuristics, so a live agent is
	//    never scattered by sub-state nor stolen into Dashboards by its goal or
	//    preview text. Managed agents split only by lifecycle. Service/runtime
	//    kinds are not agents even when they carry an @oc_agent label.
	if sessionHasManagedAgent(session) {
		return agentLifecycleGroup(m, session, state)
	}

	// 3. Non-agent sessions: genuine services, dashboards, and viewers match on
	//    chrome text (name/window/title/command) only — never goal or preview.
	if sessionHasViewerKind(session) {
		return groupViewers
	}
	if sessionIsService(session) {
		return groupServices
	}
	chrome := sessionChromeText(session)
	if containsAny(chrome, "tmuxwatch", "cass-agents", "dashboard", " mux ") {
		return groupDashboard
	}
	if sessionHasViewerKind(session) ||
		containsAny(chrome, "-html", "localhost", "http://", "vite", "library-matrix") {
		return groupViewers
	}

	// 4. Sessions that look like agent runs by name but carry no managed metadata
	//    (e.g. a raw committee/codex shell) still route by lifecycle.
	if containsAny(chrome, agentNameTokens...) {
		return agentLifecycleGroup(m, session, state)
	}

	// 5. Remaining non-agent work.
	if stateIsCompletedInfo(state) || state == "held" || state == "stale" || sessionAllPanesDead(session) {
		return groupCompletedAgents
	}
	if stateNeedsAttention(state) {
		return groupSubsystemFailures
	}
	if isShellOnly(session) {
		return groupIdle
	}
	return groupWork
}

// janitorJoinState classifies how a sidecar row relates to the session it is
// named after. Only janitorJoinOK grants teardown/countdown authority; the
// mismatch and missing-identity states exist so the UI can say why a row was
// rejected instead of silently attaching stale cleanup truth by name.
type janitorJoinState int

const (
	janitorJoinNone            janitorJoinState = iota // no fresh row for this session name
	janitorJoinOK                                      // row identity matches a current pane
	janitorJoinMissingIdentity                         // row carries no pane_id/pane_created
	janitorJoinMismatch                                // row identity matches no current pane
)

// janitorSessionRow returns the janitor sidecar row for a session when the
// sidecar is fresh ("ok") AND the row's pane identity matches a current pane
// in that session. A missing, stale, or invalid sidecar yields no row, and a
// name-only match (identity absent or pointing at a replaced pane) yields the
// row with a non-OK join state: Cockpit renders a janitor-health or identity
// warning elsewhere and must never infer cleanup eligibility from absent or
// stale facts.
func (m *Model) janitorSessionRow(session tmux.Session) (janitorSessionStatus, janitorJoinState) {
	if m == nil || m.janitorStatus.State != "ok" || len(m.janitorStatus.Sessions) == 0 {
		return janitorSessionStatus{}, janitorJoinNone
	}
	row, ok := m.janitorStatus.Sessions[session.Name]
	if !ok {
		return janitorSessionStatus{}, janitorJoinNone
	}
	return row, janitorRowJoin(row, session)
}

// janitorRowCarriesCleanupAuthority reports whether an ignored (mismatched or
// identity-less) sidecar row would have changed cleanup presentation, so the
// card can warn about exactly the rows whose rejection matters instead of
// stamping every card that merely has an "active" row.
func janitorRowCarriesCleanupAuthority(row janitorSessionStatus) bool {
	switch strings.ToLower(strings.TrimSpace(row.JanitorState)) {
	case "marked_for_teardown", "cleanup_pending", "cleanup_blocked", "protected":
		return true
	}
	return strings.TrimSpace(row.KillNotBefore) != "" ||
		strings.TrimSpace(row.MarkedAt) != "" ||
		strings.TrimSpace(row.LastRefusal) != ""
}

// janitorRowJoin validates a sidecar row's pane identity against the current
// snapshot session. pane_id is the primary key; pane_created (which hygiene
// populates from the primary pane's #{session_created}) guards pane-id
// recycling across tmux server restarts, so it may match either the pane's or
// the session's creation time.
func janitorRowJoin(row janitorSessionStatus, session tmux.Session) janitorJoinState {
	paneID := strings.TrimSpace(row.PaneID)
	createdRaw := strings.TrimSpace(row.PaneCreated)
	if paneID == "" || createdRaw == "" {
		return janitorJoinMissingIdentity
	}
	created, err := strconv.ParseInt(createdRaw, 10, 64)
	if err != nil {
		return janitorJoinMissingIdentity
	}
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.ID != paneID {
				continue
			}
			if pane.PID != "" && row.PanePID == "" {
				return janitorJoinMissingIdentity
			}
			if pane.PID != row.PanePID {
				return janitorJoinMismatch
			}
			if created == pane.CreatedAt.Unix() || created == session.CreatedAt.Unix() {
				return janitorJoinOK
			}
			return janitorJoinMismatch
		}
	}
	return janitorJoinMismatch
}

// agentLifecycleGroup places an agent session by the lifecycle-contract
// presentation precedence (docs/lifecycle-contract.md, Signal Precedence):
//
//  1. live operator/approval prompt        -> Active Agents
//  2. live/resumed work (incl. live+held)  -> Active Agents
//  3. failed/terminal problem in window    -> Failed Agents
//  4. explicit hold on non-live work       -> Held / Teardown Blocked
//     (a held+marked conflict is held, never a clean countdown)
//  5. janitor refusal/blocker              -> Cleanup Blocked
//  6. valid janitor mark, no conflict      -> Marked For Teardown
//  7. completed/delivered-idle, unmarked   -> Completed Agent Runs
//
// Janitor facts come from the status sidecar when fresh; tmux @oc_* metadata
// is the fallback signal. Only actually-marked sessions may appear under
// Marked For Teardown.
func agentLifecycleGroup(m *Model, session tmux.Session, state string) cockpitGroup {
	row, join := m.janitorSessionRow(session)
	hasRow := join == janitorJoinOK
	held := sessionHasHold(session, m.clockNow()) || (state == "held" && !sessionAllPanesDead(session))
	blocked := hasRow && strings.EqualFold(strings.TrimSpace(row.JanitorState), "cleanup_blocked")
	if stateIsCompletedInfo(state) && retainedUntilExit(row) {
		blocked = false
	}
	marked := state == "marked-for-teardown" ||
		(hasRow && strings.EqualFold(strings.TrimSpace(row.JanitorState), "marked_for_teardown"))
	// A validly joined sidecar mark must still lose to genuine live/operator
	// evidence (Signal Precedence rows 1-2): resumed real work stays active
	// and the mark renders only as cleanup metadata.
	if marked && sessionHasLiveEvidence(m, session) {
		return groupActiveAgents
	}

	if state == "awaiting-operator" {
		return groupActiveAgents
	}
	if stateIsTerminalProblem(state) {
		// A failed pane the janitor refuses on evidence grounds is past its
		// visible window and permanently stuck: show the blocker, not an
		// immortal failure card.
		if blocked && !held {
			return groupCleanupBlocked
		}
		return groupFailedAgents
	}
	// Non-live cleanup debt: completed/delivered-idle/stale/quiet, an explicit
	// janitor mark, or a dead pane with no completed/terminal signal.
	if stateIsCompletedInfo(state) || state == "idle-finished" || marked ||
		state == "stale" || state == "quiet" || state == "held" || sessionAllPanesDead(session) {
		if held {
			return groupHeldAgents
		}
		if blocked {
			return groupCleanupBlocked
		}
		if marked {
			return groupMarkedForTeardown
		}
		return groupCompletedAgents
	}
	// Live managed agent: waiting/blocked/review and any unknown sub-state stay
	// in the active band, never scattered.
	return groupActiveAgents
}

// verdictIsGenuineLiveEvidence reports whether a lifecycle verdict is backed
// by captured pane content (an active marker or a real operator prompt), as
// opposed to the low-confidence metadata-live fallback. Only evidence-backed
// verdicts may outrank teardown marks.
func verdictIsGenuineLiveEvidence(verdict paneLifecycleVerdict) bool {
	switch verdict.state {
	case "live-working":
		for _, reason := range verdict.reasons {
			if reason == "active-marker" {
				return true
			}
		}
		return false
	case "awaiting-operator":
		return true
	default:
		return false
	}
}

// sessionHasLiveEvidence reports whether any live pane in the session carries
// evidence-backed live-working/operator-prompt content. agentLifecycleGroup
// uses it so a validly joined sidecar mark cannot pull genuinely resumed work
// out of Active Agents (hygiene should cancel that mark on its next cycle).
func sessionHasLiveEvidence(m *Model, session tmux.Session) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.Dead {
				continue
			}
			verdict := m.cachedLifecycleVerdict(pane, session)
			if verdictIsGenuineLiveEvidence(verdict) {
				return true
			}
		}
	}
	return false
}

// Presentation only: an expired dead-worker lease is no longer a hold.
// Live work remains conservatively retained pending the janitor's checks.
func paneHasDisplayHold(pane tmux.Pane, now time.Time) bool {
	if pane.Cockpit == nil || strings.TrimSpace(pane.Cockpit.HoldReason) == "" {
		return false
	}
	switch pane.Cockpit.Kind {
	case "service", "viewer", "runtime":
		return true
	}
	until := parseCockpitTimestamp(pane.Cockpit.HoldUntil)
	return until.IsZero() || now.Before(until) || !pane.Dead
}

func sessionHasHold(session tmux.Session, now time.Time) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if paneHasDisplayHold(pane, now) {
				return true
			}
		}
	}
	return false
}

func sessionAttentionState(m *Model, session tmux.Session) string {
	entry := m.classifyEntry(session.ID)
	if entry != nil && entry.attentionKnown {
		return entry.attention
	}
	state := computeSessionAttentionState(m, session)
	if entry != nil {
		entry.attention = state
		entry.attentionKnown = true
	}
	return state
}

func computeSessionAttentionState(m *Model, session tmux.Session) string {
	best := ""
	bestRank := 100
	managedOnly := sessionHasManagedAgent(session)
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if managedOnly && !paneHasAgentIdentity(pane) {
				continue
			}
			state := paneAttentionState(m, session, pane)
			rank := attentionRank(state)
			if rank < bestRank {
				best = state
				bestRank = rank
			}
		}
	}
	return best
}

func paneAttentionState(m *Model, session tmux.Session, pane tmux.Pane) string {
	// Explicit assignment completion is not overwritten by stale screen text.
	if pane.Dead && pane.DeadStatus != 0 {
		return "failed"
	}
	if state := assignmentTerminalState(pane.Cockpit); state != "" {
		return state
	}

	// Genuine live evidence outranks teardown marks (lifecycle-contract Signal
	// Precedence rows 1-2): a pane whose captured content proves live work or
	// an operator prompt stays active even when pane metadata or a sidecar row
	// still carries a stale mark. The mark itself keeps rendering as
	// non-authoritative cleanup metadata on the card (cockpitCleanupLine).
	// Metadata-only live states (@oc_state=running with no content evidence)
	// and lower-priority verdicts (delivered-idle, failed, ...) do NOT outrank
	// a mark.
	var verdict paneLifecycleVerdict
	if !pane.Dead {
		verdict = m.cachedLifecycleVerdict(pane, session)
		if verdictIsGenuineLiveEvidence(verdict) {
			if verdict.state == "live-working" {
				return "running"
			}
			return verdict.state
		}
	}
	if pane.Cockpit != nil {
		switch strings.ToLower(strings.TrimSpace(pane.Cockpit.JanitorState)) {
		case "marked_for_teardown", "cleanup_pending":
			return "marked-for-teardown"
		}
		if strings.TrimSpace(pane.Cockpit.TeardownMarkedAt) != "" {
			return "marked-for-teardown"
		}
	}
	if !pane.Dead {
		switch verdict.state {
		case "live-working":
			// Metadata-only live signal: kept, but only after mark checks.
			return "running"
		case "awaiting-operator", "delivered-idle", "failed", "terminal-done", "terminal-problem", "stale":
			return verdict.state
		}
	}
	if outcome := m.semanticPaneOutcome(pane); outcome.state != "" {
		return outcome.state
	}
	if isOpenClawRuntimePane(pane) {
		state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
		switch state {
		case "failed", "blocked", "running", "done", "review", "unknown":
			return state
		case "":
			return "review"
		default:
			return state
		}
	}
	if pane.Dead && pane.DeadStatus != 0 {
		return "failed"
	}
	if pane.Dead && pane.Cockpit != nil && !pane.Cockpit.DisplayOnly() {
		// A dead agent that was still held belongs in Completed, not System
		// Problems: the hold means "don't reap", and it has finished.
		if strings.TrimSpace(pane.Cockpit.HoldReason) != "" {
			return "held"
		}
		state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
		switch state {
		case "", "starting", "running", "waiting", "blocked", "unknown":
			return "stale"
		}
	}
	if pane.Cockpit != nil {
		if pane.Cockpit.DisplayOnly() {
			if pane.Dead {
				return "done"
			}
			if m != nil && m.isStale(session.ID) && isQuietLiveServiceSession(session) {
				return "quiet"
			}
			if m != nil && m.isStale(session.ID) {
				return "stale"
			}
		}
		switch strings.ToLower(strings.TrimSpace(pane.Cockpit.JanitorState)) {
		case "protected", "cleanup_refused":
			if strings.TrimSpace(pane.Cockpit.HoldReason) != "" {
				return "held"
			}
		}
		state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
		// A live managed agent TUI that has finished its work but idles at a
		// prompt is reclassified (read-only, presentation-only) as idle-finished
		// so it renders under Completed instead of appearing to still run.
		switch state {
		case "failed", "route-fail", "safety-fail", "review", "blocked", "waiting", "running", "starting", "done", "pass", "signal", "directional", "null-safe", "held", "stale":
			return state
		}
		if state == "" && strings.TrimSpace(pane.Cockpit.HoldReason) != "" && pane.Dead {
			return "held"
		}
	}
	if pane.Dead {
		return "done"
	}
	if m != nil && m.isStale(session.ID) {
		if isServiceSession(session) {
			return "quiet"
		}
		return "stale"
	}
	return "running"
}

func attentionRank(state string) int {
	switch state {
	case "failed", "route-fail", "safety-fail":
		return 0
	case "terminal-problem":
		return 0
	case "awaiting-operator", "blocked", "waiting", "review":
		return 1
	case "running", "starting":
		return 2
	case "done", "held", "stale", "pass", "signal", "directional", "null-safe", "idle-finished", "delivered-idle", "terminal-done", "marked-for-teardown":
		return 3
	default:
		return 4
	}
}

func sessionHasCockpitAgent(session tmux.Session) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.Cockpit == nil {
				continue
			}
			agent := strings.TrimSpace(pane.Cockpit.Agent)
			kind := strings.ToLower(strings.TrimSpace(pane.Cockpit.Kind))
			if agent != "" || isAgentKind(kind) {
				return true
			}
		}
	}
	return false
}

// sessionHasManagedAgent reports whether a session carries a managed *agent*
// lifecycle — unlike sessionHasCockpitAgent it excludes service/runtime kinds,
// which can carry an @oc_agent label without being active agent runs. This
// is the identity signal used for cockpit grouping so services are never pulled
// into the Active Agents band.
func sessionHasManagedAgent(session tmux.Session) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.Cockpit == nil {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(pane.Cockpit.Kind))
			if kind == "service" || kind == "detected-service" || kind == "runtime" || kind == "viewer" || kind == "detected-viewer" {
				continue
			}
			agent := strings.TrimSpace(pane.Cockpit.Agent)
			if agent != "" || isAgentKind(kind) {
				return true
			}
		}
	}
	return false
}

// sessionIsService reports a genuine service session: matched either by chrome
// keywords (name/window/title/command) or by an explicit service-kind pane.
func sessionIsService(session tmux.Session) bool {
	if isServiceSession(session) {
		return true
	}
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.Cockpit != nil && strings.EqualFold(strings.TrimSpace(pane.Cockpit.Kind), "service") {
				return true
			}
		}
	}
	return false
}

func sessionHasViewerKind(session tmux.Session) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if pane.Cockpit == nil {
				continue
			}
			kind := strings.ToLower(strings.TrimSpace(pane.Cockpit.Kind))
			if kind == "viewer" || kind == "detected-viewer" {
				return true
			}
		}
	}
	return false
}

// sessionChromeText returns the low-noise identifying text for a session: its
// name, window names, pane titles, and running commands. It deliberately
// EXCLUDES cockpit goal/presentation fields and pane preview text so keyword
// matching can never steal an agent by its goal or transcript contents (the
// v0.9.4 theft vector). This is the only text used for non-agent
// dashboard/viewer/service and agent-by-name keyword matching.
func sessionChromeText(session tmux.Session) string {
	var b strings.Builder
	b.WriteString(session.Name)
	b.WriteByte(' ')
	b.WriteString(session.Name)
	for _, window := range session.Windows {
		b.WriteByte(' ')
		b.WriteString(window.Name)
		for _, pane := range window.Panes {
			b.WriteByte(' ')
			b.WriteString(pane.CurrentCmd)
			b.WriteByte(' ')
			b.WriteString(pane.Title)
		}
	}
	return strings.ToLower(b.String())
}

func isServiceSession(session tmux.Session) bool {
	return containsAny(sessionChromeText(session), "go2rtc", "frigate", "camera", "pantry", "detector", "alerts", "notification-watcher", "smonitor")
}

// sessionRuntimePresentationGroup returns the presentationGroup of a session's
// first OpenClaw runtime pane, or "" when the session has no runtime pane. Used
// by view filters that must still distinguish route/handoff cards after the
// runtime display bands were consolidated into one group.
func sessionRuntimePresentationGroup(session tmux.Session) string {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if isOpenClawRuntimePane(pane) {
				return strings.ToLower(strings.TrimSpace(pane.Cockpit.PresentationGroup))
			}
		}
	}
	return ""
}

func isQuietLiveServiceSession(session tmux.Session) bool {
	return (sessionIsService(session) || sessionHasViewerKind(session)) && !sessionAllPanesDead(session)
}

func isShellOnly(session tmux.Session) bool {
	found := false
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			found = true
			cmd := strings.ToLower(strings.TrimSpace(pane.CurrentCmd))
			if cmd != "" && cmd != "zsh" && cmd != "bash" && cmd != "sh" && cmd != "fish" {
				return false
			}
		}
	}
	return found
}

func sessionHasOpenClawRuntime(session tmux.Session) bool {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if isOpenClawRuntimePane(pane) {
				return true
			}
		}
	}
	return false
}

func isOpenClawRuntimePane(pane tmux.Pane) bool {
	return pane.Cockpit != nil && strings.EqualFold(strings.TrimSpace(pane.Cockpit.ManagedBy), "openclaw_runtime_snapshot")
}

func openClawRuntimeGroupFor(session tmux.Session) (cockpitGroup, bool) {
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			if !isOpenClawRuntimePane(pane) {
				continue
			}
			// Presentation group is the authoritative routing signal. Runtime
			// work-item failures go to Operational Failures; route/source/platform
			// health failures go to Sub-System Failures.
			switch strings.ToLower(strings.TrimSpace(pane.Cockpit.PresentationGroup)) {
			case "needs_decision", "current_work", "delivery_handoff":
				return groupOperationalFailures, true
			case "route_health", "source_unknown", "expected_controls", "skeletons":
				return groupSubsystemFailures, true
			case "completed":
				return groupCompletedAgents, true
			}
			// Fallback: no presentationGroup mapping hit. Route the raw
			// displayGroup; a needs_attention card becomes operational when it is
			// decision-like, otherwise sub-system.
			switch strings.ToLower(strings.TrimSpace(pane.Cockpit.DisplayGroup)) {
			case "needs_attention":
				return runtimeNeedsAttentionFallback(pane), true
			case "active":
				return groupOperationalFailures, true
			case "completed", "unknown":
				return groupCompletedAgents, true
			default:
				state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
				if stateNeedsAttention(state) {
					return runtimeNeedsAttentionFallback(pane), true
				}
				if stateIsLiveAgentState(state) {
					return groupOperationalFailures, true
				}
				return groupCompletedAgents, true
			}
		}
	}
	return cockpitGroup{}, false
}

// runtimeNeedsAttentionFallback routes a raw displayGroup=needs_attention card
// that had no presentationGroup mapping: operator decisions to Operational
// Failures, platform-ish failures to Sub-System Failures.
func runtimeNeedsAttentionFallback(pane tmux.Pane) cockpitGroup {
	if runtimeCardIsDecisionLike(pane) {
		return groupOperationalFailures
	}
	return groupSubsystemFailures
}

func runtimeCardIsDecisionLike(pane tmux.Pane) bool {
	if pane.Cockpit == nil {
		return false
	}
	meta := pane.Cockpit
	if strings.EqualFold(strings.TrimSpace(meta.Actionability), "operator_action") {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(meta.State)) {
	case "review", "waiting", "blocked":
		return true
	}
	// A card that names a next/suggested action for the operator is a decision.
	return strings.TrimSpace(meta.NextAction) != "" || strings.TrimSpace(meta.SuggestedAction) != ""
}

func containsAny(text string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(text, needle) {
			return true
		}
	}
	return false
}

func sortSessionsForCockpit(m *Model, sessions []tmux.Session) {
	sort.SliceStable(sessions, func(i, j int) bool {
		left := sessions[i]
		right := sessions[j]
		leftGroup := cockpitGroupFor(m, left)
		rightGroup := cockpitGroupFor(m, right)
		if leftGroup.rank != rightGroup.rank {
			return leftGroup.rank < rightGroup.rank
		}
		leftDead := sessionAllPanesDead(left)
		rightDead := sessionAllPanesDead(right)
		if leftDead != rightDead {
			return !leftDead
		}
		leftActivity := sessionLatestActivity(left)
		rightActivity := sessionLatestActivity(right)
		if !leftActivity.Equal(rightActivity) {
			return leftActivity.After(rightActivity)
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}

func orderedCockpitGroups(m *Model, sessions []tmux.Session) []cockpitGroup {
	if !m.organized || m.viewMode != viewModeOverview {
		return nil
	}
	seen := make(map[string]cockpitGroup)
	for _, group := range primaryCockpitGroups() {
		seen[group.name] = group
	}
	for _, session := range sessions {
		group := cockpitGroupFor(m, session)
		seen[group.name] = group
	}
	groups := make([]cockpitGroup, 0, len(seen))
	for _, group := range seen {
		groups = append(groups, group)
	}
	sort.SliceStable(groups, func(i, j int) bool {
		if groups[i].rank != groups[j].rank {
			return groups[i].rank < groups[j].rank
		}
		return groups[i].name < groups[j].name
	})
	return groups
}

func primaryCockpitGroups() []cockpitGroup {
	return []cockpitGroup{
		groupActiveAgents,
		groupHeldAgents,
		groupMarkedForTeardown,
		groupCleanupBlocked,
		groupFailedAgents,
		groupOperationalFailures,
		groupSubsystemFailures,
		groupServices,
	}
}

// A valid completion timestamp paired with a terminal assignment state is a
// producer fact. Untimestamped/older metadata and screen heuristics are not.
func assignmentTerminalState(meta *tmux.CockpitMeta) string {
	if meta == nil || meta.DisplayOnly() {
		return ""
	}
	completed := parseCockpitTimestamp(meta.CompletedAt)
	if completed.IsZero() {
		return ""
	}
	if started := parseCockpitTimestamp(meta.StartedAt); !started.IsZero() && completed.Before(started) {
		return ""
	}
	state := strings.ToLower(strings.TrimSpace(meta.State))
	if stateIsCompletedInfo(state) || stateIsTerminalProblem(state) {
		return state
	}
	return ""
}
func retainedUntilExit(row janitorSessionStatus) bool {
	return row.JanitorState == "retained_until_exit" || row.LastRefusal == "live_session_requires_explicit_retirement" || row.Reason == "live_session_requires_explicit_retirement"
}
