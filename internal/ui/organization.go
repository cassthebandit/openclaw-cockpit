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
	groupNeedsInput    = cockpitGroup{name: "Needs Input", rank: 0}
	groupFailed        = cockpitGroup{name: "Failed", rank: 1}
	groupRunningAgents = cockpitGroup{name: "Running Agents", rank: 2}
	groupWork          = cockpitGroup{name: "Active Work", rank: 3}
	groupServices      = cockpitGroup{name: "Services", rank: 4}
	groupDashboard     = cockpitGroup{name: "Dashboards", rank: 5}
	groupViewers       = cockpitGroup{name: "Viewers", rank: 6}
	groupDoneHeld      = cockpitGroup{name: "Completed / Held", rank: 7}
	groupIdle          = cockpitGroup{name: "Idle / Unowned", rank: 8}
)

func cockpitGroupFor(m *Model, session tmux.Session) cockpitGroup {
	state := sessionAttentionState(m, session)
	switch state {
	case "failed":
		return groupFailed
	case "waiting", "blocked":
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
	if state == "done" || state == "held" || state == "stale" || sessionAllPanesDead(session) {
		return groupDoneHeld
	}
	if sessionHasCockpitAgent(session) || containsAny(text, "committee", "fable", "codex", "claude", "gemini", "agy", "antigravity", "opencode", "aider") {
		return groupRunningAgents
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
		case "failed", "blocked", "waiting", "running", "starting", "done", "stale":
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
	case "failed":
		return 0
	case "blocked", "waiting":
		return 1
	case "running", "starting":
		return 2
	case "done", "held", "stale":
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
			if agent != "" || kind == "agent" || kind == "smoke" {
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
