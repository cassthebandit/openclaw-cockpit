// File cards.go renders session preview cards and their visual chrome.
package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"
	"github.com/mattn/go-runewidth"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func decorateControl(label string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColorFocus)).
		Render(label)
}

func resizePreviewViewport(preview *sessionPreview, width, height int) {
	if preview == nil || preview.viewport == nil {
		return
	}
	shouldFollow := preview.autoFollow || preview.viewport.AtBottom()
	changed := false
	if width > 0 && preview.viewport.Width() != width {
		preview.viewport.SetWidth(width)
		changed = true
	}
	if height >= 0 && preview.viewport.Height() != height {
		preview.viewport.SetHeight(height)
		changed = true
	}
	if changed && shouldFollow {
		preview.viewport.GotoBottom()
		preview.autoFollow = true
	}
}

// renderSessionPreviews lays out each visible session card with consistent
// sizing and mouse hit-test metadata.
func (m *Model) renderSessionPreviews(int) string {
	return m.renderSessionCards(m.filteredSessions())
}

// renderSessionCards renders the card wall for an already filtered/sorted
// session list, letting View reuse one computation per frame.
func (m *Model) renderSessionCards(sessions []tmux.Session) string {
	m.cardLayout = m.cardLayout[:0]
	m.groupZones = m.groupZones[:0]
	m.cardTopLine = make(map[string]int)
	m.cardLineHeight = make(map[string]int)
	organizedOverview := m.organized && m.viewMode == viewModeOverview
	if len(sessions) == 0 && !organizedOverview {
		m.cursorSession = ""
		return ""
	}

	groups := []cockpitGroup(nil)
	if organizedOverview {
		groups = orderedCockpitGroups(m, sessions)
		m.seedGroupCollapse(groups)
	}

	cols := max(1, m.cardCols)
	m.ensureCursor(m.gridSessionsFrom(sessions))
	baseStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColorBase)).
		Padding(0, cardPadding)

	innerHeight := m.cardInnerHeight
	if innerHeight < 0 {
		innerHeight = 0
	}

	rendered := make([]string, 0)
	currentRow := make([]string, 0, cols)
	currentRowIDs := make([]string, 0, cols)
	lineCursor := 0
	now := m.clockNow()
	groupCounts := sessionGroupCounts(m, sessions)
	groupHeights := m.cardBodyHeightsByGroup(sessions)
	currentGroup := ""
	currentGroupCollapsed := false
	currentCols := cols
	currentInnerWidth := m.cardInnerWidth
	if currentInnerWidth < 1 {
		currentInnerWidth = max(20, (m.width/currentCols)-(cardPadding*2+2))
	}
	currentCellWidth := currentInnerWidth + cardPadding*2 + 2

	flushRow := func() {
		if len(currentRow) == 0 {
			return
		}
		padded := make([]string, 0, len(currentRow)*2-1)
		for i, card := range currentRow {
			padded = append(padded, lipgloss.NewStyle().Width(currentCellWidth).Render(card))
			if cardColumnGap > 0 && i < len(currentRow)-1 {
				padded = append(padded, strings.Repeat(" ", cardColumnGap))
			}
		}
		rowStr := lipgloss.JoinHorizontal(lipgloss.Left, padded...)
		rowLines := countLines(rowStr)
		for _, id := range currentRowIDs {
			m.cardTopLine[id] = lineCursor
			m.cardLineHeight[id] = rowLines
		}
		// Backfill hit-test geometry for this row's cardLayout entries (they
		// are appended in the same order as currentRowIDs, before the flush).
		// cardAt falls back to this when windowing clipped a card's zone.
		if base := len(m.cardLayout) - len(currentRowIDs); base >= 0 {
			for i := range currentRowIDs {
				cb := &m.cardLayout[base+i]
				cb.screenX0 = i * (currentCellWidth + cardColumnGap)
				cb.screenX1 = cb.screenX0 + currentCellWidth - 1
				cb.gridTop = lineCursor
				cb.gridHeight = rowLines
				cb.hasGeometry = true
			}
		}
		rendered = append(rendered, rowStr)
		lineCursor += rowLines
		currentRow = currentRow[:0]
		currentRowIDs = currentRowIDs[:0]
	}

	groupIndex := 0
	renderDividerForGroup := func(group cockpitGroup) {
		flushRow()
		currentCols, currentInnerWidth = m.cardLayoutForGroup(group, max(1, groupCounts[group.name]))
		currentCellWidth = currentInnerWidth + cardPadding*2 + 2
		currentGroupCollapsed = m.isGroupCollapsed(group.name)
		summary := ""
		if currentGroupCollapsed {
			summary = m.groupCollapsedSummary(group, sessions)
		}
		divider := m.renderGroupDivider(group, groupCounts[group.name], currentGroupCollapsed, summary)
		rendered = append(rendered, divider)
		lineCursor += countLines(divider)
		currentGroup = group.name
	}

	for _, session := range sessions {
		window, ok := activeWindow(session)
		if !ok {
			continue
		}
		pane, ok := activePane(window)
		if !ok {
			continue
		}
		innerWidth := currentInnerWidth
		if organizedOverview {
			group := cockpitGroupFor(m, session)
			if group.name != currentGroup {
				for groupIndex < len(groups) && groups[groupIndex].name != group.name {
					renderDividerForGroup(groups[groupIndex])
					groupIndex++
				}
				if groupIndex < len(groups) {
					renderDividerForGroup(groups[groupIndex])
					groupIndex++
				} else {
					renderDividerForGroup(group)
				}
				innerWidth = currentInnerWidth
			}
			if currentGroupCollapsed {
				// Collapsed group: render the divider only, skip its cards.
				continue
			}
		}

		preview, ok := m.previews[session.ID]
		if !ok {
			continue
		}

		resizePreviewViewport(preview, innerWidth, preview.viewport.Height())

		pulsing := now.Sub(preview.lastChanged) < pulseDuration
		stale := m.isStale(session.ID)
		if sessionHasOpenClawRuntime(session) || isQuietLiveServiceSession(session) {
			stale = false
		}
		focused := session.ID == m.focusedSession
		cursor := session.ID == m.cursorSession
		hovered := session.ID == m.hoveredSession

		cardID := fmt.Sprintf("%scard:%s", m.zonePrefix, session.ID)
		closeID := fmt.Sprintf("%sclose:%s", m.zonePrefix, session.ID)
		maxID := fmt.Sprintf("%smax:%s", m.zonePrefix, session.ID)
		collapseID := fmt.Sprintf("%scollapse:%s", m.zonePrefix, session.ID)
		showCollapse := m.viewMode != viewModeDetail || m.detailSession != session.ID

		maxLabel := maximizeLabel
		if m.viewMode == viewModeDetail && m.detailSession == session.ID {
			maxLabel = restoreLabel
		}
		collapseDisplay := collapseLabel
		if m.isCollapsed(session.ID) {
			collapseDisplay = expandLabel
		}

		maxContent := maxLabel
		collapseContent := collapseDisplay
		closeContent := closeLabel
		if m.hoveredControl == maxID {
			maxContent = decorateControl(maxLabel)
		}
		if showCollapse && m.hoveredControl == collapseID {
			collapseContent = decorateControl(collapseDisplay)
		}
		if m.hoveredControl == closeID {
			closeContent = decorateControl(closeLabel)
		}
		controlSegments := []string{zone.Mark(maxID, maxContent)}
		if showCollapse {
			controlSegments = append(controlSegments, zone.Mark(collapseID, collapseContent))
		} else {
			collapseID = ""
		}
		controlSegments = append(controlSegments, zone.Mark(closeID, closeContent))
		controls := strings.Join(controlSegments, " ")

		sessionState := sessionAttentionState(m, session)
		bodyBudget := innerHeight
		if m.organized && m.viewMode == viewModeOverview && currentGroup != "" {
			if groupHeight, ok := groupHeights[currentGroup]; ok {
				bodyBudget = groupHeight
			}
		}
		if bodyBudget < 0 {
			bodyBudget = 0
		}
		infoLines := []string{}
		if !m.isCollapsed(session.ID) {
			infoLines = cockpitCardInfoLines(innerWidth, m, session, pane, sessionState, now)
		}
		viewportHeight := bodyBudget
		if len(infoLines) > 0 {
			viewportHeight -= len(infoLines)
		}
		if viewportHeight < 0 {
			viewportHeight = 0
		}
		resizePreviewViewport(preview, innerWidth, viewportHeight)

		header := lipgloss.NewStyle().Render(formatHeader(now, innerWidth, session, window, pane, focused, pulsing, stale, cursor, sessionState, controls, m.hostname))
		body := preview.viewport.View()
		if m.isCollapsed(session.ID) {
			body = ""
		} else if len(infoLines) > 0 {
			info := append(infoLines, body)
			body = lipgloss.JoinVertical(lipgloss.Left, info...)
		}

		borderStyle := baseStyle
		state := sessionState
		if state == "" {
			state = sessionCockpitState(m, session, pane, stale)
		}
		if body != "" {
			body = compactFinishedBody(innerWidth, body, state, bodyBudget)
			body = compactOverviewBody(innerWidth, body, m.viewMode == viewModeOverview, bodyBudget)
			body = renderCardBodyBlock(innerWidth, body, agentCLIColorPassthroughGroup(currentGroup))
		}
		switch {
		case state == "failed" || state == "route-fail" || state == "safety-fail":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitFail))
		case state == "blocked" || state == "review":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorBlocked))
		case state == "waiting":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorWaiting))
		case currentGroup == groupInactiveAgents.name && (state == "done" || state == "pass" || state == "signal" || state == "idle-finished" || state == "terminal-done" || state == "delivered-idle" || state == "marked-for-teardown"):
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(groupColorInactive))
		case state == "marked-for-teardown":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(groupColorInactive))
		case state == "done" || state == "pass" || state == "signal":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitOK))
		case state == "idle-finished":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorCursor))
		case state == "directional" || state == "null-safe":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(headerColorCursor))
		case pane.Dead && pane.DeadStatus != 0:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitFail))
		case pane.Dead:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitOK))
		case currentGroup == groupActiveAgents.name:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(groupColorActive))
		case focused:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorFocus))
		case cursor:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorCursor))
		case hovered:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorHover))
		case stale:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorStale))
		case pulsing:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorPulse))
		}

		cardContent := borderStyle.Render(lipgloss.JoinVertical(lipgloss.Left, header, body))
		cardContent = zone.Mark(cardID, cardContent)

		currentRow = append(currentRow, cardContent)
		currentRowIDs = append(currentRowIDs, session.ID)
		// Appended before the flush so flushRow can backfill this row's
		// geometry into exactly the trailing len(currentRowIDs) entries.
		m.cardLayout = append(m.cardLayout, cardBounds{
			sessionID:      session.ID,
			zoneID:         cardID,
			closeZoneID:    closeID,
			maximizeZoneID: maxID,
			collapseZoneID: collapseID,
		})
		if len(currentRow) >= currentCols {
			flushRow()
		}
	}

	flushRow()
	if organizedOverview {
		for groupIndex < len(groups) {
			renderDividerForGroup(groups[groupIndex])
			groupIndex++
		}
	}

	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *Model) cardLayoutForCount(count int) (int, int) {
	return m.cardLayoutForCountWithPolicy(count, m.preferredCols, 20)
}

func (m *Model) cardLayoutForGroup(group cockpitGroup, count int) (int, int) {
	preferredCols := m.preferredCols
	minInnerWidth := 20

	switch {
	case isDenseRuntimeGroup(group):
		preferredCols = 5
		minInnerWidth = 30
	case isSpaciousWorkGroup(group):
		preferredCols = cappedPreferredColumns(m.preferredCols, 3)
		minInnerWidth = 30
	case group.name == groupServices.name:
		preferredCols = cappedPreferredColumns(m.preferredCols, 3)
	}

	return m.cardLayoutForCountWithPolicy(count, preferredCols, minInnerWidth)
}

func cappedPreferredColumns(global int, cap int) int {
	if cap < 1 {
		return global
	}
	if global > 0 && global < cap {
		return global
	}
	return cap
}

func isDenseRuntimeGroup(group cockpitGroup) bool {
	switch group.name {
	case groupOperationalFailures.name,
		groupSubsystemFailures.name:
		return true
	default:
		return false
	}
}

func isSpaciousWorkGroup(group cockpitGroup) bool {
	switch group.name {
	case groupActiveAgents.name,
		groupInactiveAgents.name,
		groupFailedAgents.name,
		groupWork.name:
		return true
	default:
		return false
	}
}

func (m *Model) cardLayoutForCountWithPolicy(count int, preferredCols int, minInnerWidth int) (int, int) {
	const (
		columnOverhead = cardPadding*2 + 2
	)
	if minInnerWidth < 1 {
		minInnerWidth = 20
	}
	minColumnStride := minInnerWidth + columnOverhead
	if count < 1 {
		count = 1
	}
	cols := 1
	if count > 1 {
		cols = min(count, max(1, (m.width+cardColumnGap)/(minColumnStride+cardColumnGap)))
		if preferredCols > 0 {
			cols = min(cols, preferredCols)
		}
	}
	innerWidth := m.cardInnerWidth
	if m.organized && m.viewMode == viewModeOverview {
		gapWidth := (cols - 1) * cardColumnGap
		usableWidth := max(cols, m.width-gapWidth)
		innerWidth = max(minInnerWidth, (usableWidth/cols)-columnOverhead)
	}
	if innerWidth < 1 {
		gapWidth := (cols - 1) * cardColumnGap
		usableWidth := max(cols, m.width-gapWidth)
		innerWidth = max(minInnerWidth, (usableWidth/max(1, cols))-columnOverhead)
	}
	return max(1, cols), innerWidth
}

type bodyHeightConstraint struct {
	min    int
	max    int
	weight int
}

func (m *Model) cardBodyHeightsByGroup(sessions []tmux.Session) map[string]int {
	heights := make(map[string]int)
	if !m.organized || m.viewMode != viewModeOverview || len(sessions) == 0 {
		return heights
	}
	counts := sessionGroupCounts(m, sessions)
	groups := orderedCockpitGroups(m, sessions)
	if len(groups) == 0 {
		return heights
	}

	const frameHeight = 3
	available := m.previewAvailableHeight()
	fixedRows := len(groups)
	type section struct {
		group    cockpitGroup
		rows     int
		c        bodyHeightConstraint
		priority int
	}
	sections := make([]section, 0, len(groups))
	openRank := 0
	for _, group := range groups {
		count := counts[group.name]
		if count <= 0 || m.isGroupCollapsed(group.name) {
			continue
		}
		cols, _ := m.cardLayoutForGroup(group, count)
		rows := (count + cols - 1) / cols
		if rows < 1 {
			rows = 1
		}
		fixedRows += rows * frameHeight
		c := bodyHeightConstraintForGroup(group)
		sections = append(sections, section{
			group:    group,
			rows:     rows,
			c:        c,
			priority: openGroupBodyPriority(openRank, c.weight),
		})
		openRank++
	}
	if len(sections) == 0 {
		return heights
	}
	bodyRows := available - fixedRows
	if bodyRows < 1 {
		bodyRows = 1
	}

	used := 0
	for _, section := range sections {
		height := section.c.min
		if height < 1 {
			height = 1
		}
		if section.c.max > 0 && height > section.c.max {
			height = section.c.max
		}
		heights[section.group.name] = height
		used += height * section.rows
	}
	for used > bodyRows {
		best := -1
		bestWeight := int(^uint(0) >> 1)
		for i, section := range sections {
			height := heights[section.group.name]
			if height <= 1 {
				continue
			}
			if section.c.weight < bestWeight {
				best = i
				bestWeight = section.c.weight
			}
		}
		if best < 0 {
			break
		}
		section := sections[best]
		heights[section.group.name]--
		used -= section.rows
	}

	slack := bodyRows - used
	for slack > 0 {
		best := -1
		bestPriority := -1
		for i, section := range sections {
			height := heights[section.group.name]
			if section.c.max > 0 && height >= section.c.max {
				continue
			}
			cost := section.rows
			if cost <= 0 || cost > slack {
				continue
			}
			if section.priority > bestPriority {
				best = i
				bestPriority = section.priority
			}
		}
		if best < 0 {
			break
		}
		section := sections[best]
		heights[section.group.name]++
		slack -= section.rows
	}
	if slack > 0 && len(sections) > 0 {
		// If every soft cap is satisfied, spend remaining screen real estate on
		// the first expanded populated accordion. This matches the operator
		// workflow: the top open section is the work surface, not a peer panel.
		top := sections[0]
		for top.rows > 0 && slack >= top.rows {
			heights[top.group.name]++
			slack -= top.rows
		}
	}
	return heights
}

func (m *Model) previewAvailableHeight() int {
	if m.height <= 0 {
		return minPreviewHeight
	}
	offset := m.previewOffset
	if offset <= 0 || offset >= m.height {
		offset = topPaddingLines
	}
	footerHeight := max(1, m.footerHeight)
	available := m.height - offset - footerHeight
	if available < 1 {
		return 1
	}
	return available
}

func openGroupBodyPriority(openRank int, categoryWeight int) int {
	if categoryWeight < 0 {
		categoryWeight = 0
	}
	switch openRank {
	case 0:
		return 1200 + categoryWeight
	case 1:
		return 500 + categoryWeight
	case 2:
		return 300 + categoryWeight
	default:
		return max(100-openRank*10, 10) + categoryWeight
	}
}

func bodyHeightConstraintForGroup(group cockpitGroup) bodyHeightConstraint {
	switch group.name {
	case groupActiveAgents.name:
		return bodyHeightConstraint{min: 12, max: 64, weight: 8}
	case groupHeldAgents.name:
		return bodyHeightConstraint{min: 8, max: 32, weight: 5}
	case groupInactiveAgents.name:
		return bodyHeightConstraint{min: 8, max: 32, weight: 5}
	case groupCleanupBlocked.name:
		return bodyHeightConstraint{min: 8, max: 32, weight: 5}
	case groupFailedAgents.name:
		return bodyHeightConstraint{min: 8, max: 32, weight: 6}
	case groupOperationalFailures.name:
		return bodyHeightConstraint{min: minPreviewHeight, max: 24, weight: 4}
	case groupSubsystemFailures.name:
		return bodyHeightConstraint{min: minPreviewHeight, max: 24, weight: 3}
	case groupWork.name:
		return bodyHeightConstraint{min: 12, max: 64, weight: 6}
	case groupDoneHeld.name:
		return bodyHeightConstraint{min: 3, max: 7, weight: 1}
	case groupServices.name:
		return bodyHeightConstraint{min: 4, max: 8, weight: 1}
	case groupDashboard.name, groupViewers.name, groupIdle.name:
		return bodyHeightConstraint{min: 4, max: 10, weight: 1}
	default:
		return bodyHeightConstraint{min: minPreviewHeight, max: 24, weight: 2}
	}
}

func sessionGroupCounts(m *Model, sessions []tmux.Session) map[string]int {
	counts := make(map[string]int)
	if !m.organized || m.viewMode != viewModeOverview {
		return counts
	}
	for _, session := range sessions {
		counts[cockpitGroupFor(m, session).name]++
	}
	return counts
}

// gridSessions returns the filtered sessions that are actually laid out as cards
// — i.e. filtered sessions minus any that live in a collapsed accordion group.
// Card rendering and cursor navigation both use this so a collapsed group's
// cards are neither drawn nor selectable, while its divider still shows.
func (m *Model) gridSessions() []tmux.Session {
	return m.gridSessionsFrom(m.filteredSessions())
}

// gridSessionsFrom applies the collapsed-group filter to an already computed
// filtered session list.
func (m *Model) gridSessionsFrom(sessions []tmux.Session) []tmux.Session {
	if !m.organized || m.viewMode != viewModeOverview {
		return sessions
	}
	out := make([]tmux.Session, 0, len(sessions))
	for _, session := range sessions {
		if m.isGroupCollapsed(cockpitGroupFor(m, session).name) {
			continue
		}
		out = append(out, session)
	}
	return out
}

// summaryStateLabel folds a raw attention state into a short label for a
// collapsed-group summary chip.
func summaryStateLabel(state string) string {
	switch state {
	case "":
		return ""
	case "route-fail", "safety-fail":
		return "failed"
	case "idle-finished":
		return "finished"
	case "awaiting-operator":
		return "prompt"
	case "delivered-idle":
		return "delivered"
	case "terminal-done":
		return "done"
	case "terminal-problem":
		return "failed"
	default:
		return state
	}
}

// groupCollapsedSummary builds a compact "N failed · N done"-style summary of
// the notable member states for a collapsed group divider.
func (m *Model) groupCollapsedSummary(group cockpitGroup, sessions []tmux.Session) string {
	counts := map[string]int{}
	for _, session := range sessions {
		if cockpitGroupFor(m, session).name != group.name {
			continue
		}
		label := summaryStateLabel(sessionAttentionState(m, session))
		if label == "" {
			continue
		}
		counts[label]++
	}
	order := []string{"failed", "prompt", "blocked", "waiting", "review", "running", "starting", "delivered", "finished", "done", "pass", "held", "stale", "quiet"}
	parts := make([]string, 0, 2)
	for _, label := range order {
		if n := counts[label]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, label))
			if len(parts) == 2 {
				break
			}
		}
	}
	return strings.Join(parts, " · ")
}

func (m *Model) renderGroupDivider(group cockpitGroup, count int, collapsed bool, summary string) string {
	width := m.width
	if width < 1 {
		width = 1
	}
	caret := groupCaretExpanded
	if collapsed {
		caret = groupCaretCollapsed
	}
	accentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(groupAccentColor(group))).
		Bold(true)
	labelStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColorBase)).
		Bold(true)
	mutedStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("244"))

	text := accentStyle.Render(caret)
	text += " " + labelStyle.Render(group.name)
	text += accentStyle.Render(fmt.Sprintf("  %d", count))
	if collapsed && summary != "" {
		text += mutedStyle.Render(" · " + summary)
	}
	bar := lipgloss.NewStyle().
		Width(width).
		Padding(0, 1).
		Render(text)
	if m.zonePrefix != "" {
		zoneID := fmt.Sprintf("%sgroup:%s", m.zonePrefix, group.name)
		m.groupZones = append(m.groupZones, groupZone{name: group.name, zoneID: zoneID})
		bar = zone.Mark(zoneID, bar)
	}
	return bar
}

func groupAccentColor(group cockpitGroup) string {
	switch group.name {
	case groupActiveAgents.name:
		return groupColorActive
	case groupHeldAgents.name:
		return groupColorInactive
	case groupInactiveAgents.name:
		return groupColorInactive
	case groupCleanupBlocked.name:
		return groupColorInactive
	case groupFailedAgents.name:
		return groupColorFailed
	case groupOperationalFailures.name:
		return groupColorOperational
	case groupSubsystemFailures.name:
		return groupColorSubsystem
	case groupServices.name:
		return groupColorServices
	default:
		return "250"
	}
}

// formatHeader builds the label line for a session card, colouring it based on
// status and focus state.
func formatHeader(now time.Time, width int, session tmux.Session, window tmux.Window, pane tmux.Pane, focused, pulsing, stale, cursor bool, attentionState string, controls string, host string) string {
	var meta []string
	hasDoneTiming := false
	if pane.Dead {
		meta = append(meta, pane.StatusString())
	}
	if !pane.LastActivity.IsZero() {
		meta = append(meta, fmt.Sprintf("last %s", coarseDuration(now.Sub(pane.LastActivity))))
	}
	if pane.Cockpit != nil {
		if doneAt := parseCockpitTimestamp(pane.Cockpit.CompletedAt); !doneAt.IsZero() {
			meta = append(meta, fmt.Sprintf("done %s", coarseDuration(now.Sub(doneAt))))
			hasDoneTiming = true
		} else if startedAt := parseCockpitTimestamp(pane.Cockpit.StartedAt); !startedAt.IsZero() {
			meta = append(meta, fmt.Sprintf("launched %s", coarseDuration(now.Sub(startedAt))))
		}
	}
	state := attentionState
	if state == "" {
		state = cockpitState(pane, stale)
	}
	runtimeHeader := isOpenClawRuntimePane(pane)
	if state != "" && state != "running" && state != "starting" && state != "quiet" && !(state == "done" && hasDoneTiming) && !runtimeHeader {
		meta = append(meta, attentionStateLabel(state))
	}
	titleParts := cockpitTitleParts(session, window, pane, host)
	if badge := cockpitGroupBadge(pane.Cockpit); badge != "" {
		titleParts = append(titleParts, badge)
	}
	label := strings.Join(titleParts, " · ")
	if stale && state == "" {
		meta = append(meta, "stale")
	}

	if len(meta) > 0 {
		label += " · " + strings.Join(meta, " · ")
	}

	spaceForLabel := width - lipgloss.Width(controls)
	if spaceForLabel < 1 {
		spaceForLabel = 1
	}
	label = truncateSingleLine(label, spaceForLabel)
	padding := spaceForLabel - lipgloss.Width(label)
	if padding < 0 {
		padding = 0
	}
	header := label + strings.Repeat(" ", padding) + controls
	style := lipgloss.NewStyle()
	switch {
	case state == "failed" || state == "route-fail" || state == "safety-fail":
		style = style.Foreground(lipgloss.Color(headerColorExitFail))
	case state == "blocked" || state == "review":
		style = style.Foreground(lipgloss.Color(headerColorBlocked))
	case state == "waiting":
		style = style.Foreground(lipgloss.Color(headerColorWaiting))
	case state == "marked-for-teardown":
		style = style.Foreground(lipgloss.Color(headerColorWaiting))
	case state == "done" || state == "pass" || state == "signal":
		style = style.Foreground(lipgloss.Color(headerColorExitOK))
	case state == "idle-finished":
		style = style.Foreground(lipgloss.Color(headerColorCursor))
	case state == "directional" || state == "null-safe":
		style = style.Foreground(lipgloss.Color(headerColorCursor))
	case pane.Dead && pane.DeadStatus != 0:
		style = style.Foreground(lipgloss.Color(headerColorExitFail))
	case pane.Dead:
		style = style.Foreground(lipgloss.Color(headerColorExitOK))
	case focused:
		style = style.Foreground(lipgloss.Color(headerColorFocus))
	case cursor:
		style = style.Foreground(lipgloss.Color(headerColorCursor))
	case stale:
		style = style.Foreground(lipgloss.Color(headerColorStale))
	case pulsing:
		style = style.Foreground(lipgloss.Color(headerColorPulse))
	default:
		style = style.Foreground(lipgloss.Color(headerColorBase))
	}
	style = style.Width(width)
	return style.Render(header)
}

func renderCardBodyBlock(width int, body string, preserveAgentCLIColors bool) string {
	if body == "" {
		return ""
	}
	style := lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("246"))
	if preserveAgentCLIColors {
		body = normalizeAgentCLIANSI(body)
	} else {
		body = stripANSI(body)
	}
	lines := strings.Split(body, "\n")
	for i, line := range lines {
		lines[i] = style.Render(line)
	}
	return strings.Join(lines, "\n")
}

func agentCLIColorPassthroughGroup(groupName string) bool {
	switch groupName {
	case groupActiveAgents.name, groupHeldAgents.name, groupInactiveAgents.name, groupCleanupBlocked.name, groupFailedAgents.name:
		return true
	default:
		return false
	}
}

func truncateSingleLine(value string, width int) string {
	value = cardSafeLine(value)
	if width <= 0 {
		return ""
	}
	if lipgloss.Width(value) <= width {
		return value
	}
	tail := "..."
	if width < lipgloss.Width(tail)+1 {
		tail = ""
	}
	return runewidth.Truncate(value, width, tail)
}

func cockpitTitleParts(session tmux.Session, window tmux.Window, pane tmux.Pane, host string) []string {
	if pane.Cockpit != nil {
		meta := pane.Cockpit
		if isOpenClawRuntimePane(pane) {
			runtime := strings.ToUpper(firstNonEmpty(meta.Agent, "runtime"))
			status := firstNonEmpty(meta.DisplayStatus, meta.State, "unknown")
			title := firstNonEmpty(meta.Goal, pane.TitleOrCmd(), session.Name)
			return dedupeTitleParts([]string{runtime, status, title})
		}
		if strings.EqualFold(strings.TrimSpace(meta.Kind), "service") {
			return dedupeTitleParts([]string{"SERVICE", session.Name})
		}
		parts := []string{}
		if meta.DisplayOnly() {
			parts = append(parts, "ADOPTED")
		}
		if agent := strings.TrimSpace(meta.Agent); agent != "" {
			parts = append(parts, strings.ToUpper(agent))
		} else if kind := strings.TrimSpace(meta.Kind); kind != "" {
			parts = append(parts, strings.ToUpper(kind))
		}
		if owner := strings.TrimSpace(meta.Owner); owner != "" {
			parts = append(parts, owner)
		}
		if project := strings.TrimSpace(meta.Project); project != "" {
			parts = append(parts, project)
		}
		if len(parts) > 0 {
			return parts
		}
	}

	titleParts := dedupeTitleParts([]string{session.Name, window.Name})
	paneLabel := strings.TrimSpace(pane.TitleOrCmd())
	if host != "" && strings.EqualFold(strings.TrimSpace(paneLabel), strings.TrimSpace(host)) {
		paneLabel = ""
	}
	if paneLabel != "" {
		titleParts = dedupeTitleParts(append(titleParts, paneLabel))
	}
	return titleParts
}

func dedupeTitleParts(parts []string) []string {
	out := make([]string, 0, len(parts))
	seen := map[string]struct{}{}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		key := strings.ToLower(part)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, part)
	}
	return out
}

func parseCockpitTimestamp(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if parsed, err := time.Parse(time.RFC3339, value); err == nil {
		return parsed
	}
	return time.Time{}
}

func cockpitState(pane tmux.Pane, stale bool) string {
	return cockpitStateWithModel(nil, pane, stale)
}

func cockpitStateWithModel(m *Model, pane tmux.Pane, stale bool) string {
	if m != nil {
		if outcome := m.semanticPaneOutcome(pane); outcome.state != "" {
			return outcome.state
		}
	} else if outcome := semanticPaneOutcome(pane); outcome.state != "" {
		return outcome.state
	}
	if pane.Dead && pane.DeadStatus != 0 {
		return "failed"
	}
	if pane.Cockpit != nil {
		if pane.Cockpit.DisplayOnly() && pane.Dead {
			return "done"
		}
		switch strings.ToLower(strings.TrimSpace(pane.Cockpit.JanitorState)) {
		case "marked_for_teardown", "cleanup_pending":
			return "marked-for-teardown"
		}
		if strings.TrimSpace(pane.Cockpit.TeardownMarkedAt) != "" {
			return "marked-for-teardown"
		}
		state := strings.ToLower(strings.TrimSpace(pane.Cockpit.State))
		if pane.Dead {
			switch state {
			case "", "starting", "running", "waiting", "blocked", "unknown":
				return "stale"
			}
		}
		switch state {
		case "starting", "running", "waiting", "blocked", "done", "failed", "route-fail", "safety-fail", "stale", "review", "pass", "signal", "directional", "null-safe", "held":
			return state
		}
	}
	if stale {
		return "stale"
	}
	return ""
}

func sessionCockpitState(m *Model, session tmux.Session, pane tmux.Pane, stale bool) string {
	if stale && isQuietLiveServiceSession(session) {
		stale = false
	}
	return cockpitStateWithModel(m, pane, stale)
}

func cockpitInfoLines(width int, m *Model, session tmux.Session, pane tmux.Pane, now time.Time) []string {
	if pane.Cockpit == nil {
		return nil
	}
	lines := []string{}
	goal := strings.TrimSpace(pane.Cockpit.Goal)
	if goal != "" {
		lines = append(lines, cockpitSubtleLine(width, "goal: "+goal))
	}
	if cleanup := cockpitCleanupLine(m, session, pane, now); cleanup != "" {
		lines = append(lines, cockpitSubtleLine(width, cleanup))
	}
	return lines
}

func cockpitCardInfoLines(width int, m *Model, session tmux.Session, pane tmux.Pane, sessionState string, now time.Time) []string {
	lines := []string{}
	if attention := cockpitAttentionLine(width, m, session, pane, sessionState); attention != "" {
		lines = append(lines, attention)
	}
	lines = append(lines, cockpitInfoLines(width, m, session, pane, now)...)
	if m != nil &&
		m.viewMode == viewModeDetail &&
		m.detailSession == session.ID &&
		isOpenClawRuntimePane(pane) {
		lines = append(lines, m.runtimeTimelineDetailLines(width, maxRuntimeTimelineDetailRows)...)
	}
	return lines
}

func cockpitAttentionLine(width int, m *Model, session tmux.Session, pane tmux.Pane, sessionState string) string {
	sessionState = strings.TrimSpace(sessionState)
	if sessionState == "" {
		return ""
	}
	activeState := paneAttentionState(m, session, pane)
	if activeState == sessionState {
		return ""
	}
	return cockpitSubtleLine(width, "attention: "+sessionState+" in another pane")
}

func cockpitSubtleLine(width int, line string) string {
	line = cardSafeLine(line)
	if width > 0 && lipgloss.Width(line) > width {
		line = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(line)
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(line)
}

// attentionStateLabel maps an internal attention state to the chip shown in the
// card header. idle-finished carries the "finished · awaiting review" wording so
// a reclassified TUI pane reads as done-and-awaiting-review, not still running.
func attentionStateLabel(state string) string {
	if state == "idle-finished" {
		return "finished · awaiting review"
	}
	if state == "marked-for-teardown" {
		return "marked for teardown"
	}
	if state == "awaiting-operator" {
		return "waiting on operator"
	}
	return state
}

func compactFinishedBody(width int, body string, state string, maxLines int) string {
	switch state {
	case "done", "held", "stale", "failed", "route-fail", "safety-fail", "review", "pass", "signal", "directional", "null-safe", "idle-finished", "marked-for-teardown":
	default:
		return body
	}
	if maxLines <= 0 {
		maxLines = maxOverviewBodyLines
	}
	lines := trimTrailingBlankLines(strings.Split(body, "\n"))
	if len(lines) <= maxLines {
		return strings.Join(lines, "\n")
	}
	hidden := len(lines) - maxLines
	kept := append([]string{}, lines[:maxLines]...)
	message := fmt.Sprintf("... %d more lines, open detail for transcript", hidden)
	if width > 0 && lipgloss.Width(message) > width {
		message = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(message)
	}
	kept = append(kept, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(message))
	return strings.Join(kept, "\n")
}

func compactOverviewBody(width int, body string, overview bool, maxLines int) string {
	if !overview {
		return body
	}
	if maxLines <= 0 {
		maxLines = maxOverviewBodyLines
	}
	lines := trimTrailingBlankLines(strings.Split(body, "\n"))
	if len(lines) == 0 {
		return ""
	}
	message := ""
	if len(lines) <= maxLines {
		for len(lines) < maxLines {
			lines = append(lines, "")
		}
		return strings.Join(lines, "\n")
	}
	keepLines := max(1, maxLines-1)
	kept := append([]string{}, lines[:keepLines]...)
	message = fmt.Sprintf("... %d more lines, open detail for full pane", len(lines)-len(kept))
	if width > 0 && lipgloss.Width(message) > width {
		message = lipgloss.NewStyle().Width(width).MaxWidth(width).Render(message)
	}
	kept = append(kept, lipgloss.NewStyle().Foreground(lipgloss.Color("244")).Render(message))
	return strings.Join(kept, "\n")
}

func trimTrailingBlankLines(lines []string) []string {
	end := len(lines)
	for end > 0 && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return append([]string{}, lines[:end]...)
}

func cockpitCleanupLine(m *Model, session tmux.Session, pane tmux.Pane, now time.Time) string {
	if pane.Cockpit == nil {
		return ""
	}
	meta := pane.Cockpit
	row, join := m.janitorSessionRow(session)
	hasRow := join == janitorJoinOK
	parts := []string{}
	if policy := strings.TrimSpace(meta.CleanupPolicy); policy != "" {
		parts = append(parts, "policy: "+policy)
	}
	if ttl := strings.TrimSpace(meta.TTL); ttl != "" && ttl != "never" {
		parts = append(parts, "ttl: "+ttl)
	}
	hold := strings.TrimSpace(meta.HoldReason)
	marked := strings.TrimSpace(meta.TeardownMarkedAt)
	if hold != "" {
		label := "hold blocks cleanup: " + hold
		if marked != "" {
			// Held+marked is a conflict: the mark is inert while the hold
			// stands, so no countdown may render next to it.
			label += " · teardown mark inert (hold conflict)"
		}
		parts = append(parts, label)
	} else if marked != "" {
		label := "marked for teardown"
		if reason := strings.TrimSpace(meta.TeardownReason); reason != "" {
			label += ": " + reason
		}
		label += " · " + janitorCountdownText(row, join, now)
		parts = append(parts, label)
	} else if strings.EqualFold(strings.TrimSpace(meta.JanitorState), "cleanup_pending") {
		parts = append(parts, "cleanup pending")
	}
	if janitorRowCarriesCleanupAuthority(row) {
		switch join {
		case janitorJoinMismatch:
			// A fresh sidecar row exists under this session's name but describes
			// a different pane: say so instead of attaching its cleanup truth.
			parts = append(parts, "janitor row ignored: pane identity mismatch (stale row for a previous pane)")
		case janitorJoinMissingIdentity:
			parts = append(parts, "janitor row ignored: no pane identity (older status payload, treated as stale)")
		}
	}
	if hasRow && strings.EqualFold(strings.TrimSpace(row.JanitorState), "cleanup_blocked") {
		reason := strings.TrimSpace(row.LastRefusal)
		if reason == "" {
			reason = strings.TrimSpace(row.Reason)
		}
		if reason == "" {
			reason = "janitor refusal"
		}
		parts = append(parts, "cleanup blocked: "+reason)
	}
	if end := strings.TrimSpace(meta.EndReason); end != "" && end != "expected_exit" {
		parts = append(parts, "end: "+end)
	}
	if progress := displayEvidencePath(meta.ProgressPath); progress != "" {
		parts = append(parts, "progress: "+progress)
	}
	if evidence := cockpitEvidenceLine(meta); evidence != "" {
		parts = append(parts, evidence)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " · ")
}

// janitorCountdownText renders the janitor-owned mark-to-kill countdown. The
// sidecar kill_not_before is the only countdown source: when the sidecar is
// missing, stale, identity-mismatched, or carries no kill_not_before, Cockpit
// says so instead of inventing a countdown from a local constant.
func janitorCountdownText(row janitorSessionStatus, join janitorJoinState, now time.Time) string {
	switch join {
	case janitorJoinOK:
		if killAt := parseCockpitTimestamp(row.KillNotBefore); !killAt.IsZero() {
			if remaining := killAt.Sub(now); remaining > 0 {
				return "cleanup in " + coarseDuration(remaining)
			}
			return "cleanup pending"
		}
		return "cleanup countdown unknown (no fresh janitor status)"
	case janitorJoinMismatch:
		return "cleanup countdown unavailable (janitor row is for a previous pane)"
	case janitorJoinMissingIdentity:
		return "cleanup countdown unavailable (janitor row has no pane identity)"
	default:
		return "cleanup countdown unknown (no fresh janitor status)"
	}
}

func cockpitGroupBadge(meta *tmux.CockpitMeta) string {
	if meta == nil {
		return ""
	}
	count := cockpitIntField(meta.GroupedRecordCount)
	if count <= 1 {
		return ""
	}
	return fmt.Sprintf("x%d", count)
}

func cockpitEvidenceLine(meta *tmux.CockpitMeta) string {
	if meta == nil {
		return ""
	}
	evidence := strings.TrimSpace(meta.EvidencePath)
	if evidence == "" {
		return ""
	}
	grouped := cockpitIntField(meta.GroupedRecordCount)
	if grouped > 1 {
		evidenceCount := countEvidenceIDs(evidence)
		if evidenceCount == 0 {
			evidenceCount = grouped
		}
		return fmt.Sprintf("evidence: %d ids, open detail for full list", evidenceCount)
	}
	if value := displayEvidencePath(evidence); value != "" {
		return "evidence: " + value
	}
	return ""
}

func countEvidenceIDs(value string) int {
	count := 0
	for _, part := range strings.Split(value, ",") {
		if strings.TrimSpace(part) != "" {
			count++
		}
	}
	return count
}

func displayEvidencePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	cleaned := filepath.Clean(value)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return ""
	}
	base := filepath.Base(cleaned)
	if base == "." || base == string(filepath.Separator) {
		return ""
	}
	return base
}
