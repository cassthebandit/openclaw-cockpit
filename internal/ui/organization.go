package ui

import (
	"sort"
	"strings"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

type cockpitGroup struct {
	name string
	rank int
}

var (
	groupNeedsInput    = cockpitGroup{name: "Needs Attention", rank: 0}
	groupFailed        = cockpitGroup{name: "Needs Attention", rank: 0}
	groupRunningAgents = cockpitGroup{name: "Active Agent Runs", rank: 1}
	groupWork          = cockpitGroup{name: "Active Work", rank: 3}
	groupDoneHeld      = cockpitGroup{name: "Completed Agent Runs", rank: 5}
	groupDashboard     = cockpitGroup{name: "Dashboards", rank: 6}
	groupViewers       = cockpitGroup{name: "Viewers", rank: 7}
	groupIdle          = cockpitGroup{name: "Idle / Unowned", rank: 8}
	groupServices      = cockpitGroup{name: "Services", rank: 9}
)

func cockpitGroupFor(m *Model, session tmux.Session) cockpitGroup {
	if group, ok := openClawRuntimeGroupFor(session); ok {
		return group
	}
	state := sessionAttentionState(m, session)
	if stateNeedsAttention(state) {
		return groupNeedsInput
	}

	text := sessionSearchText(session)
	if containsAny(text, "tmuxwatch", "cass-agents", "dashboard", " mux ") {
		return groupDashboard
	}
	if containsAny(text, "-html", "localhost", "http://", "vite", "library-matrix", "daniel-brief") {
		return groupViewers
	}
	if isServiceSession(session) {
		return groupServices
	}
	if stateIsCompletedInfo(state) || state == "held" || state == "stale" || sessionAllPanesDead(session) {
		return groupDoneHeld
	}
	if stateIsActiveRun(state) && (sessionHasCockpitAgent(session) || containsAny(text, "committee", "fable", "codex", "claude", "gemini", "agy", "antigravity", "opencode", "aider")) {
		return groupRunningAgents
	}
	if sessionHasCockpitAgent(session) || containsAny(text, "committee", "fable", "codex", "claude", "gemini", "agy", "antigravity", "opencode", "aider") {
		return groupDoneHeld
	}
	if m != nil && m.isStale(session.ID) && isShellOnly(session) {
		return groupIdle
	}
	if isShellOnly(session) {
		return groupIdle
	}
	return groupWork
}

func sessionAttentionState(m *Model, session tmux.Session) string {
	best := ""
	bestRank := 100
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
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
	if outcome := semanticPaneOutcome(pane); outcome.state != "" {
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
			if m != nil && m.isStale(session.ID) {
				return "stale"
			}
		}
		state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
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
	case "blocked", "waiting", "review":
		return 1
	case "running", "starting":
		return 2
	case "done", "held", "stale", "pass", "signal", "directional", "null-safe":
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

func sessionDetails(session tmux.Session) string {
	var b strings.Builder
	for _, window := range session.Windows {
		b.WriteByte(' ')
		b.WriteString(window.Name)
		for _, pane := range window.Panes {
			b.WriteByte(' ')
			b.WriteString(pane.CurrentCmd)
			b.WriteByte(' ')
			b.WriteString(pane.CurrentPath)
			b.WriteByte(' ')
			b.WriteString(pane.Title)
			b.WriteByte(' ')
			b.WriteString(pane.PreviewText)
			if pane.Cockpit != nil {
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.Kind)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.Agent)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.Project)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.Goal)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.State)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.DisplayStatus)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.DisplayGroup)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.Reason)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.NextAction)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.SourceKinds)
				b.WriteByte(' ')
				b.WriteString(pane.Cockpit.SourceCount)
			}
		}
	}
	return b.String()
}

func sessionSearchText(session tmux.Session) string {
	return strings.ToLower(session.Name + " " + session.Name + " " + sessionDetails(session))
}

func isServiceSession(session tmux.Session) bool {
	return containsAny(sessionSearchText(session), "go2rtc", "frigate", "camera", "pantry", "detector", "alerts", "notification-watcher", "smonitor")
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
			switch strings.ToLower(strings.TrimSpace(pane.Cockpit.DisplayGroup)) {
			case "needs_attention":
				return groupNeedsInput, true
			case "active":
				return groupRunningAgents, true
			case "completed":
				return groupDoneHeld, true
			case "unknown":
				return groupDoneHeld, true
			default:
				state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
				if stateNeedsAttention(state) {
					return groupNeedsInput, true
				}
				if stateIsActiveRun(state) {
					return groupRunningAgents, true
				}
				return groupDoneHeld, true
			}
		}
	}
	return cockpitGroup{}, false
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
