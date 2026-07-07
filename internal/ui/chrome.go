// File chrome.go defines UI chrome helpers such as search bars and headers.
package ui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// renderSearchBar prints the interactive search prompt and input box.
func renderSearchBar(input textinput.Model) string {
	label := lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("62")).Render("Search")
	return lipgloss.JoinHorizontal(lipgloss.Left, label, input.View())
}

// renderCommandBar prints the interactive Pulse view filter prompt.
func renderCommandBar(input textinput.Model) string {
	label := lipgloss.NewStyle().Padding(0, 1).Foreground(lipgloss.Color("62")).Render("Filter")
	return lipgloss.JoinHorizontal(lipgloss.Left, label, input.View())
}

// renderSearchSummary shows the current filter query when the search box is
// closed.
func renderSearchSummary(query string) string {
	return lipgloss.NewStyle().
		Padding(0, 2).
		Foreground(lipgloss.Color("62")).
		Render(fmt.Sprintf("Filter: %s (press / to edit, esc to clear)", query))
}

// renderTitleBar constructs the application header with live metadata.
func renderTitleBar(m *Model, width int) string {
	width = max(width, 1)

	base := lipgloss.NewStyle().
		Foreground(lipgloss.Color("231")).
		Background(lipgloss.Color("62"))
	name := base.Bold(true).Padding(0, 2).Render("OpenClaw Cockpit · " + m.titleViewLabel())

	metaParts := []string{m.titleSummary()}
	if !m.lastUpdated.IsZero() {
		metaParts = append(metaParts, fmt.Sprintf("refreshed %s ago", coarseDuration(time.Since(m.lastUpdated))))
	}
	if m.focusedSession != "" {
		metaParts = append(metaParts, "focus "+m.focusedSession)
	}
	if m.searchQuery != "" {
		metaParts = append(metaParts, fmt.Sprintf("filter %q", m.searchQuery))
	}
	content := name
	if len(metaParts) > 0 {
		meta := base.
			Padding(0, 2).
			Foreground(lipgloss.Color("249")).
			Render(strings.Join(metaParts, " • "))
		content = lipgloss.JoinHorizontal(lipgloss.Left, content, meta)
	}

	remaining := width - lipgloss.Width(content)
	if remaining > 0 {
		padding := base.Render(strings.Repeat(" ", remaining))
		content += padding
	}
	return content
}

func (m *Model) titleViewLabel() string {
	if m.viewMode != viewModeDetail || m.detailSession == "" {
		label := "Pulse"
		if filter := m.viewFilterLabel(); filter != "" {
			label += " / " + filter
		}
		return label
	}
	if session, ok := m.sessionByID(m.detailSession); ok {
		if strings.TrimSpace(session.Name) != "" {
			return "Detail / " + session.Name
		}
	}
	return "Detail / " + sessionLabel(m.detailSession)
}

func (m *Model) viewFilterLabel() string {
	switch m.viewFilter {
	case "decision":
		return "Decision"
	case "route":
		return "Route"
	case "handoff":
		return "Handoff"
	case "services":
		return "Services"
	default:
		return ""
	}
}

func (m *Model) titleSummary() string {
	total := len(m.sessions)
	if total == 0 {
		return "0 items"
	}

	services := 0
	staleCount := len(m.staleSessionNames())
	groupCounts := map[string]int{}
	attention := 0
	for _, session := range m.sessions {
		group := cockpitGroupFor(m, session)
		groupCounts[group.name]++
		if group.name == groupServices.name {
			services++
			continue
		}
		if stateNeedsAttention(sessionAttentionState(m, session)) {
			attention++
		}
	}

	parts := []string{fmt.Sprintf("%d items", total)}
	if attention > 0 {
		parts = append(parts, fmt.Sprintf("attention %d", attention))
	}
	// Runtime filter hints are counted by presentation group, since the
	// route/handoff/source-unknown bands now render inside one Runtime section
	// but the :route / :handoff view filters still key on presentation group.
	runtimeCounts := map[string]int{}
	for _, session := range m.sessions {
		if pg := sessionRuntimePresentationGroup(session); pg != "" {
			runtimeCounts[pg]++
		}
	}
	if n := runtimeCounts["needs_decision"]; n > 0 {
		parts = append(parts, fmt.Sprintf("decision %d", n))
	}
	if n := runtimeCounts["route_health"]; n > 0 {
		parts = append(parts, fmt.Sprintf("route %d", n))
	}
	if n := runtimeCounts["delivery_handoff"]; n > 0 {
		parts = append(parts, fmt.Sprintf("handoff %d", n))
	}
	if n := runtimeCounts["source_unknown"]; n > 0 {
		parts = append(parts, fmt.Sprintf("unknown %d", n))
	}
	if n := groupCounts[groupFailedAgents.name] + groupCounts[groupOperationalFailures.name] + groupCounts[groupSubsystemFailures.name]; n > 0 {
		parts = append(parts, fmt.Sprintf("problems %d", n))
	}
	if services > 0 {
		parts = append(parts, fmt.Sprintf("services %d", services))
	}
	if staleCount > 0 {
		parts = append(parts, fmt.Sprintf("stale %d", staleCount))
	}
	return strings.Join(parts, " · ")
}

// formatPaneVariables formats sorted tmux pane variables for display.
func formatPaneVariables(vars map[string]string) string {
	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%s", k, vars[k]))
	}
	return "vars: " + strings.Join(parts, " ")
}
