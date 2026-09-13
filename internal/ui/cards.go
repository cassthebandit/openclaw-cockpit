// File cards.go renders session preview cards and their visual chrome.
package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

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
	sessions = m.stackedSessions(sessions)
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
	leadingSlots := 0

	flushRow := func() {
		if len(currentRow) == 0 {
			return
		}
		padded := make([]string, 0, len(currentRow)*2-1)
		if leadingSlots > 0 {
			padded = append(padded, strings.Repeat(" ", leadingSlots*(currentCellWidth+cardColumnGap)))
		}
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
				cb.screenX0 = (i + leadingSlots) * (currentCellWidth + cardColumnGap)
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
		leadingSlots = 0
	}

	groupIndex := 0
	renderDividerForGroup := func(group cockpitGroup) {
		flushRow()
		currentCols, currentInnerWidth = m.cardLayoutForGroup(group, max(1, groupCounts[group.name]))
		currentCellWidth = currentInnerWidth + cardPadding*2 + 2
		leadingSlots = leadingStackSlots(groupCounts[group.name], currentCols)
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
		if pane.AlternateScreen && pane.Height > 0 {
			viewportHeight = min(viewportHeight, pane.Height)
			// Keep allocated card geometry even when native capture is shorter.
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

		borderColor := borderColorBase
		state := sessionState
		if body != "" {
			body = compactFinishedBody(innerWidth, body, state, bodyBudget)
			body = compactOverviewBody(innerWidth, body, m.viewMode == viewModeOverview, bodyBudget)
			body = preview.bodyCache.render(innerWidth, body, agentCLIColorPassthroughGroup(currentGroup))
			if organizedOverview && !m.isCollapsed(session.ID) {
				body = lipgloss.NewStyle().Height(bodyBudget).Render(body)
			}
		}
		switch {
		case state == "failed" || state == "route-fail" || state == "safety-fail":
			borderColor = borderColorExitFail
		case state == "blocked" || state == "review":
			borderColor = borderColorBlocked
		case state == "waiting":
			borderColor = borderColorWaiting
		case currentGroup == groupMarkedForTeardown.name && (state == "done" || state == "pass" || state == "signal" || state == "idle-finished" || state == "terminal-done" || state == "delivered-idle" || state == "marked-for-teardown"):
			borderColor = groupColorInactive
		case state == "marked-for-teardown":
			borderColor = groupColorInactive
		case state == "done" || state == "pass" || state == "signal":
			borderColor = borderColorExitOK
		case state == "idle-finished":
			borderColor = borderColorCursor
		case state == "directional" || state == "null-safe":
			borderColor = headerColorCursor
		case pane.Dead && pane.DeadStatus != 0:
			borderColor = borderColorExitFail
		case pane.Dead:
			borderColor = borderColorExitOK
		case currentGroup == groupActiveAgents.name:
			borderColor = groupColorActive
		case focused:
			borderColor = borderColorFocus
		case cursor:
			borderColor = borderColorCursor
		case hovered:
			borderColor = borderColorHover
		case stale:
			borderColor = borderColorStale
		case pulsing:
			borderColor = borderColorPulse
		}

		cardContent := preview.cardCache.render(header, body, borderColor)
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
		if len(currentRow)+leadingSlots >= currentCols {
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
	case groupMarkedForTeardown.name:
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
	ageLabel := ""
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
		}
		if startedAt := parseCockpitTimestamp(pane.Cockpit.StartedAt); !startedAt.IsZero() {
			ageLabel = fmt.Sprintf("launched %s", coarseDuration(now.Sub(startedAt)))
		}
	}
	if ageLabel == "" && !session.CreatedAt.IsZero() {
		ageLabel = fmt.Sprintf("session %s", coarseDuration(now.Sub(session.CreatedAt)))
	}
	state := attentionState
	runtimeHeader := isOpenClawRuntimePane(pane)
	if state != "" && state != "running" && state != "starting" && state != "quiet" && (state != "done" || !hasDoneTiming) && !runtimeHeader {
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
	if ageLabel != "" {
		// Reserve age before truncating verbose titles and metadata.
		suffix := " · " + ageLabel
		label = truncateSingleLine(label, max(1, spaceForLabel-lipgloss.Width(suffix))) + suffix
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

// Cache the expensive border composition, not the card's live inputs. Header
// and body are freshly produced before lookup, including time/focus/hover and
// viewport changes. Zone marking and geometry remain outside this cache.
type cardCompositionCache struct {
	header, body, borderColor string
	rendered                  string
}

func (c *cardCompositionCache) render(header, body, borderColor string) string {
	if c.header == header && c.body == body && c.borderColor == borderColor {
		return c.rendered
	}
	c.header, c.body, c.borderColor = header, body, borderColor
	c.rendered = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderColor)).Padding(0, cardPadding).
		Render(lipgloss.JoinVertical(lipgloss.Left, header, body))
	return c.rendered
}

// One rendered body per preview; headers, time labels, borders and hitboxes
// are still rebuilt. Exact input equality includes scroll/resize/info changes.
// The entry goes away with its preview and never accumulates old frames.
type cardBodyCache struct {
	width          int
	body           string
	preserveColors bool
	rendered       string
}

func (c *cardBodyCache) render(width int, body string, preserveColors bool) string {
	if c.width == width && c.body == body && c.preserveColors == preserveColors {
		return c.rendered
	}
	c.width, c.body, c.preserveColors = width, body, preserveColors
	c.rendered = renderCardBodyBlock(width, body, preserveColors)
	return c.rendered
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
	case groupActiveAgents.name, groupHeldAgents.name, groupMarkedForTeardown.name, groupCleanupBlocked.name, groupFailedAgents.name:
		return true
	default:
		return false
	}
}
