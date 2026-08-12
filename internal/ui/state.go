// File state.go manages tab state, detail navigation, and collapse toggles.
package ui

import (
	"fmt"

	"charm.land/lipgloss/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// tabTitles derives the current tab titles based on overview and detail state.
var (
	tabActiveStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("231")).
			Background(lipgloss.Color("62")).
			Bold(true).
			Padding(0, 1).
			MarginRight(1)

	tabInactiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(headerColorCursor)).
				Padding(0, 1).
				MarginRight(1)
)

func (m *Model) tabTitles() []string {
	m.tabSessionIDs = m.tabSessionIDs[:0]
	if m.organized {
		// Organized mode renders no per-session tabs, so the filtered slice
		// is never consumed here; skipping it avoids a full filter+classify+
		// sort pass on every frame (F7).
		titles := make([]string, 1, 2)
		titles[0] = "Pulse"
		if m.viewMode == viewModeDetail && m.detailSession != "" {
			if session, ok := m.sessionByID(m.detailSession); ok {
				// Tab labels are terminal chrome built from untrusted session
				// names: strict-sanitize them.
				label := cardSafeLine(session.Name)
				if label == "" {
					label = sessionLabel(session.ID)
				}
				m.tabSessionIDs = append(m.tabSessionIDs, session.ID)
				return append(titles, label)
			}
		}
		return titles
	}
	sessions := m.filteredSessionsFull()
	titles := make([]string, 1, len(sessions)+1)
	titles[0] = "Pulse"
	for _, session := range sessions {
		label := cardSafeLine(session.Name)
		if label == "" {
			label = sessionLabel(session.ID)
		}
		m.tabSessionIDs = append(m.tabSessionIDs, session.ID)
		titles = append(titles, label)
	}
	return titles
}

// renderTabBar produces the tab strip view based on the active selection.
func (m *Model) renderTabBar(width int) string {
	titles := m.tabTitles()
	m.applyActiveTabSelection(len(titles))
	if len(titles) == 0 {
		return ""
	}
	type tabSegment struct {
		rendered string
		width    int
		zoneID   string
	}
	segments := make([]tabSegment, 0, len(titles))
	for i, title := range titles {
		style := tabInactiveStyle
		if i == m.activeTab {
			style = tabActiveStyle
		}
		zoneID := fmt.Sprintf("%s###tab:%d", m.zonePrefix, i)
		rendered := style.Render(title)
		segments = append(segments, tabSegment{
			rendered: rendered,
			width:    lipgloss.Width(rendered),
			zoneID:   zoneID,
		})
	}
	targetWidth := max(1, width)
	rows := make([]string, 0, len(segments))
	current := make([]string, 0, len(segments))
	currentWidth := 0
	for _, seg := range segments {
		marked := zone.Mark(seg.zoneID, seg.rendered)
		if currentWidth > 0 && currentWidth+seg.width > targetWidth {
			row := lipgloss.JoinHorizontal(lipgloss.Left, current...)
			rowWidth := max(targetWidth, lipgloss.Width(row))
			rows = append(rows, lipgloss.NewStyle().Width(rowWidth).Render(row))
			current = current[:0]
			currentWidth = 0
		}
		current = append(current, marked)
		currentWidth += seg.width
	}
	if len(current) > 0 {
		row := lipgloss.JoinHorizontal(lipgloss.Left, current...)
		rowWidth := max(targetWidth, lipgloss.Width(row))
		rows = append(rows, lipgloss.NewStyle().Width(rowWidth).Render(row))
	}
	return lipgloss.JoinVertical(lipgloss.Left, rows...)
}

// shiftActiveTab moves the active tab by the provided delta, clamping to range.
func (m *Model) shiftActiveTab(delta int) {
	m.setActiveTab(m.activeTab + delta)
}

// setActiveTab activates the provided tab index and adjusts the view mode.
func (m *Model) setActiveTab(idx int) {
	m.activeTab = idx
	titles := m.tabTitles()
	m.applyActiveTabSelection(len(titles))
}

// enterDetail switches the UI into detail mode for the given session.
func (m *Model) enterDetail(sessionID string) {
	if sessionID == "" {
		return
	}
	if _, ok := m.sessionByID(sessionID); !ok {
		return
	}
	m.detailSession = sessionID
	m.viewMode = viewModeDetail
	m.focusedSession = sessionID
	m.cursorSession = sessionID
	titles := m.tabTitles()
	idx := m.indexForSession(sessionID)
	if idx == 0 {
		idx = 1
	}
	m.activeTab = idx
	m.applyActiveTabSelection(len(titles))
}

// leaveDetail returns to overview mode and optionally clears detail metadata.
func (m *Model) leaveDetail(clear bool) {
	m.viewMode = viewModeOverview
	m.activeTab = 0
	if clear {
		m.detailSession = ""
	}
}

// handleDetailToggle toggles the detail view for the selected session.
func (m *Model) handleDetailToggle(sessionID string) {
	if sessionID == "" {
		return
	}
	if m.viewMode == viewModeDetail && m.detailSession == sessionID {
		m.leaveDetail(true)
		m.tabTitles()
		m.applyActiveTabSelection(len(m.tabSessionIDs) + 1)
		return
	}
	m.enterDetail(sessionID)
}

func (m *Model) indexForSession(id string) int {
	for i, sessionID := range m.tabSessionIDs {
		if sessionID == id {
			return i + 1
		}
	}
	return 0
}

func (m *Model) applyActiveTabSelection(length int) {
	if length <= 0 {
		m.activeTab = 0
		m.viewMode = viewModeOverview
		m.detailSession = ""
		return
	}
	if m.activeTab < 0 {
		m.activeTab = 0
	}
	if m.activeTab >= length {
		m.activeTab = length - 1
	}
	if m.activeTab == 0 || len(m.tabSessionIDs) == 0 {
		m.viewMode = viewModeOverview
		m.detailSession = ""
		return
	}
	index := m.activeTab - 1
	if index >= len(m.tabSessionIDs) {
		index = len(m.tabSessionIDs) - 1
		m.activeTab = index + 1
	}
	if index < 0 {
		m.activeTab = 0
		m.viewMode = viewModeOverview
		m.detailSession = ""
		return
	}
	sessionID := m.tabSessionIDs[index]
	m.detailSession = sessionID
	m.viewMode = viewModeDetail
	if m.focusedSession == "" {
		m.focusedSession = sessionID
	}
	if m.cursorSession == "" {
		m.cursorSession = sessionID
	}
}

// sessionByID locates a session by identifier if it exists in the snapshot.
func (m *Model) sessionByID(id string) (tmux.Session, bool) {
	for _, session := range m.sessions {
		if session.ID == id {
			return session, true
		}
	}
	return tmux.Session{}, false
}

// sessionExists reports whether the snapshot currently contains the session.
func (m *Model) sessionExists(id string) bool {
	_, ok := m.sessionByID(id)
	return ok
}

// isCollapsed reports whether the session card body is collapsed.
func (m *Model) isCollapsed(id string) bool {
	_, ok := m.collapsed[id]
	return ok
}

// toggleCollapsed flips the collapse state for the supplied session.
func (m *Model) toggleCollapsed(id string) {
	if id == "" {
		return
	}
	if m.isCollapsed(id) {
		delete(m.collapsed, id)
		return
	}
	m.collapsed[id] = struct{}{}
}

// clearCollapsed expands all sessions by clearing the collapsed set.
func (m *Model) clearCollapsed() {
	m.collapsed = make(map[string]struct{})
}

// defaultExpandedGroups are the accordion sections that start expanded. Every
// other group defaults collapsed. Problem groups only appear when non-empty, so
// "expanded when non-empty" falls out of a plain expanded default.
var defaultExpandedGroups = map[string]struct{}{
	groupActiveAgents.name:        {},
	groupHeldAgents.name:          {},
	groupInactiveAgents.name:      {},
	groupCleanupBlocked.name:      {},
	groupFailedAgents.name:        {},
	groupOperationalFailures.name: {},
	groupSubsystemFailures.name:   {},
}

// isGroupCollapsed reports whether an accordion group is collapsed.
func (m *Model) isGroupCollapsed(name string) bool {
	_, ok := m.collapsedGroups[name]
	return ok
}

// toggleGroupCollapsed flips the collapse state for a group divider.
func (m *Model) toggleGroupCollapsed(name string) {
	if name == "" {
		return
	}
	if m.seededGroups == nil {
		m.seededGroups = make(map[string]struct{})
	}
	m.seededGroups[name] = struct{}{}
	if m.isGroupCollapsed(name) {
		delete(m.collapsedGroups, name)
		return
	}
	m.collapsedGroups[name] = struct{}{}
}

// seedGroupCollapse applies the default collapse state to each group exactly
// once (tracked in seededGroups), so defaults land the first time a group
// appears while user toggles persist across ticks. New groups appearing later
// get their default when first seen.
func (m *Model) seedGroupCollapse(groups []cockpitGroup) {
	if m.collapsedGroups == nil {
		m.collapsedGroups = make(map[string]struct{})
	}
	if m.seededGroups == nil {
		m.seededGroups = make(map[string]struct{})
	}
	for _, group := range groups {
		if _, done := m.seededGroups[group.name]; done {
			continue
		}
		m.seededGroups[group.name] = struct{}{}
		// Config may override a group's default accordion state; manual
		// operator toggles still win afterward because seeding runs once.
		if override, ok := m.groupCollapseOverride[group.name]; ok {
			if override {
				m.collapsedGroups[group.name] = struct{}{}
			}
			continue
		}
		if _, expanded := defaultExpandedGroups[group.name]; !expanded {
			m.collapsedGroups[group.name] = struct{}{}
		}
	}
}

// setAllGroupsCollapsed collapses or expands every currently visible group and
// marks them seeded so the choice persists.
func (m *Model) setAllGroupsCollapsed(groups []cockpitGroup, collapsed bool) {
	if m.collapsedGroups == nil {
		m.collapsedGroups = make(map[string]struct{})
	}
	if m.seededGroups == nil {
		m.seededGroups = make(map[string]struct{})
	}
	for _, group := range groups {
		m.seededGroups[group.name] = struct{}{}
		if collapsed {
			m.collapsedGroups[group.name] = struct{}{}
		} else {
			delete(m.collapsedGroups, group.name)
		}
	}
}

// anyGroupExpanded reports whether at least one of the given groups is expanded.
func (m *Model) anyGroupExpanded(groups []cockpitGroup) bool {
	for _, group := range groups {
		if !m.isGroupCollapsed(group.name) {
			return true
		}
	}
	return false
}
