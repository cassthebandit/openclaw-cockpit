package ui

import (
	"sort"
	"strings"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

type cockpitGroup struct {
	name string
	rank int
}

var (
	// Rank 0 — live/resumable agent TUIs: managed agents in a live sub-state
	// (starting/running/waiting/blocked/review), plus prompt/approval screens
	// that can continue when answered.
	groupActiveAgents = cockpitGroup{name: "Active Agents", rank: 0}
	// Rank 1 — alive or held agent panes that are no longer doing work.
	groupInactiveAgents = cockpitGroup{name: "Inactive Agents", rank: 1}
	// Rank 2 — failed/problem agent panes.
	groupFailedAgents = cockpitGroup{name: "Failed Agents", rank: 2}
	// Rank 3 — workflow/runtime failures that need operator judgment.
	groupOperationalFailures = cockpitGroup{name: "Operational Failures", rank: 3}
	// Rank 4 — platform, route, skeleton, or source-health failures.
	groupSubsystemFailures = cockpitGroup{name: "Sub-System Failures", rank: 4}
	// Rank 5 — healthy long-running watchers/bridges/monitors.
	groupServices = cockpitGroup{name: "Services", rank: 5}
	// Rank 6 — completed runtime cards and non-agent held/done panes.
	groupDoneHeld = cockpitGroup{name: "Completed Agent Runs", rank: 6}
	// Rank 7+ — non-agent fallback work, self-monitoring UIs, viewers, and shells.
	groupWork      = cockpitGroup{name: "Active Work", rank: 7}
	groupDashboard = cockpitGroup{name: "Dashboards", rank: 8}
	groupViewers   = cockpitGroup{name: "Viewers", rank: 9}
	groupIdle      = cockpitGroup{name: "Idle / Unowned", rank: 10}
)

// agentNameTokens identify an agent/review session by its chrome (name, window,
// title, command) when it carries no managed cockpit metadata.
var agentNameTokens = []string{
	"committee", "fable", "codex", "claude", "gemini",
	"agy", "antigravity", "opencode", "aider",
}

func cockpitGroupFor(m *Model, session tmux.Session) cockpitGroup {
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
		return agentLifecycleGroup(session, state)
	}

	// 3. Non-agent sessions: genuine services, dashboards, and viewers match on
	//    chrome text (name/window/title/command) only — never goal or preview.
	if sessionIsService(session) {
		return groupServices
	}
	chrome := sessionChromeText(session)
	if containsAny(chrome, "tmuxwatch", "cass-agents", "dashboard", " mux ") {
		return groupDashboard
	}
	if containsAny(chrome, "-html", "localhost", "http://", "vite", "library-matrix", "daniel-brief") {
		return groupViewers
	}

	// 4. Sessions that look like agent runs by name but carry no managed metadata
	//    (e.g. a raw committee/codex shell) still route by lifecycle.
	if containsAny(chrome, agentNameTokens...) {
		return agentLifecycleGroup(session, state)
	}

	// 5. Remaining non-agent work.
	if stateIsCompletedInfo(state) || state == "held" || state == "stale" || sessionAllPanesDead(session) {
		return groupDoneHeld
	}
	if stateNeedsAttention(state) {
		return groupSubsystemFailures
	}
	if isShellOnly(session) {
		return groupIdle
	}
	return groupWork
}

// agentLifecycleGroup places an agent session by its lifecycle only: live and
// resumable agents lead in Active Agents, failed agents route to Failed Agents,
// and completed/held/dead-clean agents remain visible as Inactive cleanup debt.
func agentLifecycleGroup(session tmux.Session, state string) cockpitGroup {
	// Explicit completed/terminal states win regardless of process liveness.
	if stateIsCompletedInfo(state) || state == "idle-finished" || state == "held" {
		return groupInactiveAgents
	}
	if state == "awaiting-operator" {
		return groupActiveAgents
	}
	if stateIsTerminalProblem(state) {
		return groupFailedAgents
	}
	// A dead agent with no completed/terminal signal is finished, not live —
	// this keeps a dead-but-"review" pane out of the active band.
	if sessionAllPanesDead(session) {
		return groupInactiveAgents
	}
	if state == "stale" || state == "quiet" {
		return groupInactiveAgents
	}
	// Live managed agent: waiting/blocked/review and any unknown sub-state stay
	// in the active band, never scattered.
	return groupActiveAgents
}

func sessionAttentionState(m *Model, session tmux.Session) string {
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
	if !pane.Dead {
		var verdict paneLifecycleVerdict
		if m != nil {
			verdict = m.cachedLifecycleVerdict(pane, session)
		} else {
			verdict = paneLifecycleVerdictFor(pane, sessionHasManagedAgent(session) || containsAny(sessionChromeText(session), agentNameTokens...))
		}
		switch verdict.state {
		case "live-working":
			return "running"
		case "awaiting-operator", "delivered-idle", "failed", "terminal-done", "terminal-problem", "stale":
			return verdict.state
		}
	}
	if m != nil {
		if outcome := m.semanticPaneOutcome(pane); outcome.state != "" {
			return outcome.state
		}
	} else if outcome := semanticPaneOutcome(pane); outcome.state != "" {
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
	case "done", "held", "stale", "pass", "signal", "directional", "null-safe", "idle-finished", "delivered-idle", "terminal-done":
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
			kind := strings.TrimSpace(pane.Cockpit.Kind)
			if agent != "" || kind == "agent" || kind == "batch-worker" || kind == "smoke" {
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
			if kind == "service" || kind == "runtime" {
				continue
			}
			agent := strings.TrimSpace(pane.Cockpit.Agent)
			if agent != "" || kind == "agent" || kind == "batch-worker" || kind == "smoke" {
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
	return isServiceSession(session) && !sessionAllPanesDead(session)
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
				return groupDoneHeld, true
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
				return groupDoneHeld, true
			default:
				state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
				if stateNeedsAttention(state) {
					return runtimeNeedsAttentionFallback(pane), true
				}
				if stateIsLiveAgentState(state) {
					return groupOperationalFailures, true
				}
				return groupDoneHeld, true
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
		groupInactiveAgents,
		groupFailedAgents,
		groupOperationalFailures,
		groupSubsystemFailures,
		groupServices,
	}
}
