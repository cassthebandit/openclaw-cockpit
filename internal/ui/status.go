// File status.go renders the status footer, stale indicators, and toast
// messages.
package ui

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// renderStatus returns the cached status footer, recomputing when necessary.
func (m *Model) renderStatus() string {
	content := m.buildStatusLine(m.width)
	if content == m.cachedStatus {
		return m.cachedStatus
	}
	m.cachedStatus = content
	return content
}

// buildStatusLine assembles the footer lines detailing input helpers, stale
// sessions, pane variables, toasts, and errors.
func (m *Model) buildStatusLine(width int) string {
	// The footer states the authority the session actually has. Labelling a
	// key-forwarding session "monitor-only" would let an operator misjudge
	// whether typing reaches the selected pane.
	// Control mode is deliberately partial (dashboard-reserved keys are not
	// forwarded); the label says so instead of promising transparent typing.
	authority := "control mode (partial)"
	if m.monitorOnly {
		authority = "monitor-only"
	}
	helper := fmt.Sprintf("%s · mouse scroll/click · %s/d detail · %s/%s collapse · keys / search, :/v filter, H hidden, q quit", authority, maximizeLabel, collapseLabel, expandLabel)
	if m.monitorOnly {
		helper += " · no cleanup/key forwarding"
	} else {
		helper += " · cleanup via session_hygiene.py or safe_kill.py"
	}
	lines := []string{
		lipgloss.NewStyle().
			Foreground(lipgloss.Color("245")).
			Padding(0, 2).
			Render(helper),
	}

	if summary := m.formatCockpitSummary(width); summary != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 2).
			Render(summary))
	}

	if timeline := m.formatRuntimeTimelineLine(width); timeline != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 2).
			Render(timeline))
	}

	if janitor := m.janitorStatusLine(width); janitor != "" {
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("250")).
			Padding(0, 2).
			Render(janitor))
	}

	if m.paneParseWarnings > 0 {
		warning := fmt.Sprintf("warning: %d pane row(s) hidden due to tmux parse errors", m.paneParseWarnings)
		lines = append(lines, lipgloss.NewStyle().
			Foreground(lipgloss.Color("209")).
			Padding(0, 2).
			Render(warning))
	}

	if stale := m.staleSessionNames(); len(stale) > 0 {
		staleLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("246")).
			Padding(0, 2).
			Render(formatStaleLine(stale, width))
		lines = append(lines, staleLine)
	}

	if m.viewMode == viewModeDetail {
		if preview, ok := m.previews[m.detailSession]; ok && len(preview.vars) > 0 {
			varsLine := lipgloss.NewStyle().
				Foreground(lipgloss.Color("244")).
				Padding(0, 2).
				Render(formatPaneVariables(preview.vars))
			lines = append(lines, varsLine)
		}
	} else if m.focusedSession != "" {
		varsLine := lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")).
			Padding(0, 2).
			Render(fmt.Sprintf("focused: %s · press d for detail vars", sessionLabel(m.focusedSession)))
		lines = append(lines, varsLine)
	}

	if m.err != nil {
		// Error text can embed external tmux/runtime output: strict-sanitize
		// before it becomes footer chrome.
		errPart := lipgloss.NewStyle().
			Foreground(lipgloss.Color("203")).
			Padding(0, 2).
			Render("Error: " + cardSafeLine(m.err.Error()))
		lines = append(lines, errPart)
	}
	if toast := m.toastView(m.width); toast != "" {
		lines = append(lines, toast)
	}

	return lipgloss.JoinVertical(lipgloss.Left, lines...)
}

func (m *Model) formatCockpitSummary(width int) string {
	counts := map[string]int{}
	total := 0
	skeletons := 0
	suppressed := 0
	grouped := 0
	rawCards := 0
	visibleCards := 0
	totalVisibleCards := 0
	groupedCards := 0
	hiddenCards := 0
	for _, session := range m.sessions {
		if !sessionHasCockpitAgent(session) {
			continue
		}
		state := sessionAttentionState(m, session)
		if state == "" {
			state = "running"
		}
		state = m.cockpitSummaryState(session, state)
		counts[state]++
		total++
		for _, window := range session.Windows {
			for _, pane := range window.Panes {
				if pane.Cockpit == nil {
					continue
				}
				if strings.EqualFold(strings.TrimSpace(pane.Cockpit.Skeleton), "true") {
					skeletons++
				}
				if strings.EqualFold(strings.TrimSpace(pane.Cockpit.Suppressed), "true") {
					suppressed++
				}
				if cockpitGroupedRecordCount(pane) > 1 {
					grouped++
				}
				rawCards = max(rawCards, cockpitIntField(pane.Cockpit.RawCardCount))
				visibleCards = max(visibleCards, cockpitIntField(pane.Cockpit.VisibleCardCount))
				totalVisibleCards = max(totalVisibleCards, cockpitIntField(pane.Cockpit.TotalVisibleCardCount))
				groupedCards = max(groupedCards, cockpitIntField(pane.Cockpit.GroupedCardCount))
				hiddenCards = max(hiddenCards, cockpitIntField(pane.Cockpit.HiddenCardCount))
			}
		}
	}
	if total == 0 {
		return ""
	}

	order := []string{
		"failed", "route-fail", "safety-fail", "review",
		"waiting", "blocked", "cleanup-blocked", "running", "starting",
		"done", "pass", "signal", "directional", "null-safe", "held", "stale", "quiet",
		"marked-for-teardown",
	}
	parts := []string{fmt.Sprintf("cockpit items: %d", total)}
	for _, state := range order {
		if n := counts[state]; n > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", state, n))
		}
		delete(counts, state)
	}
	remainingStates := make([]string, 0, len(counts))
	for state, n := range counts {
		if n > 0 {
			remainingStates = append(remainingStates, state)
		}
	}
	sort.Strings(remainingStates)
	for _, state := range remainingStates {
		parts = append(parts, fmt.Sprintf("%s %d", state, counts[state]))
	}
	if skeletons > 0 {
		parts = append(parts, fmt.Sprintf("skeletons %d", skeletons))
	}
	if suppressed > 0 {
		parts = append(parts, fmt.Sprintf("suppressed %d", suppressed))
	}
	if grouped > 0 {
		parts = append(parts, fmt.Sprintf("grouped %d", grouped))
	}
	if rawCards > 0 || visibleCards > 0 || groupedCards > 0 || hiddenCards > 0 {
		cardParts := []string{}
		if rawCards > 0 {
			cardParts = append(cardParts, fmt.Sprintf("raw %d", rawCards))
		}
		if visibleCards > 0 {
			cardParts = append(cardParts, fmt.Sprintf("shown %d", visibleCards))
		}
		if totalVisibleCards > visibleCards {
			parts = append(parts, fmt.Sprintf("total %d (truncated)", totalVisibleCards))
		}
		if groupedCards > 0 {
			cardParts = append(cardParts, fmt.Sprintf("pulse groups %d", groupedCards))
		}
		if hiddenCards > 0 {
			cardParts = append(cardParts, fmt.Sprintf("hidden %d", hiddenCards))
		}
		parts = append(parts, strings.Join(cardParts, "/"))
	}
	line := strings.Join(parts, " · ")
	if width > 0 && lipgloss.Width(line) > width {
		return lipgloss.NewStyle().Width(width).MaxWidth(width).Render(line)
	}
	return line
}

func (m *Model) cockpitSummaryState(session tmux.Session, state string) string {
	group := agentLifecycleGroup(m, session, state)
	switch group.name {
	case groupHeldAgents.name:
		return "held"
	case groupCleanupBlocked.name:
		return "cleanup-blocked"
	case groupInactiveAgents.name:
		return "marked-for-teardown"
	default:
		return state
	}
}

func cockpitGroupedRecordCount(pane tmux.Pane) int {
	if pane.Cockpit == nil {
		return 0
	}
	return cockpitIntField(pane.Cockpit.GroupedRecordCount)
}

func cockpitIntField(value string) int {
	var n int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &n); err != nil {
		return 0
	}
	return n
}

func formatStaleLine(names []string, width int) string {
	prefix := "stale sessions: "
	suffix := " (cleanup: session_hygiene.py or safe_kill.py)"
	if len(names) == 0 {
		return prefix + suffix
	}

	maxWidth := width
	if maxWidth <= 0 {
		maxWidth = lipgloss.Width(prefix) + lipgloss.Width(suffix) + 80
	}
	budget := maxWidth - lipgloss.Width(prefix) - lipgloss.Width(suffix)
	if budget < 0 {
		budget = 0
	}

	displayed := make([]string, 0, len(names))
	remaining := 0
	for i, name := range names {
		candidate := strings.Join(append(displayed, name), ", ")
		if lipgloss.Width(candidate) > budget && len(displayed) > 0 {
			remaining = len(names) - i
			break
		}
		displayed = append(displayed, name)
	}

	body := strings.Join(displayed, ", ")
	if remaining > 0 {
		if body != "" {
			body += " …"
		} else {
			body = "…"
		}
		body += fmt.Sprintf(" (+%d more)", remaining)
	}

	line := prefix + body + suffix
	// Final guard in case nothing fit.
	if body == "" {
		line = prefix + suffix
	}
	return line
}
