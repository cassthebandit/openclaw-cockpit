// File cards.go renders session preview cards and their visual chrome.
package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/mattn/go-runewidth"
	zone "github.com/steipete/tmuxwatch/internal/zone"

	"github.com/steipete/tmuxwatch/internal/tmux"
)

func decorateControl(label string) string {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(headerColorFocus)).
		Render(label)
}

// renderSessionPreviews lays out each visible session card with consistent
// sizing and mouse hit-test metadata.
func (m *Model) renderSessionPreviews(offset int) string {
	sessions := m.filteredSessions()
	m.cardLayout = m.cardLayout[:0]
	if len(sessions) == 0 {
		m.cursorSession = ""
		return ""
	}

	cols := max(1, m.cardCols)
	m.ensureCursor(sessions)
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
	now := time.Now()
	groupCounts := sessionGroupCounts(m, sessions)
	groupHeights := m.cardBodyHeightsByGroup(sessions)
	currentGroup := ""
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
		rendered = append(rendered, lipgloss.JoinHorizontal(lipgloss.Left, padded...))
		currentRow = currentRow[:0]
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
		if m.organized && m.viewMode == viewModeOverview {
			group := cockpitGroupFor(m, session)
			if group.name != currentGroup {
				flushRow()
				currentCols, currentInnerWidth = m.cardLayoutForCount(groupCounts[group.name])
				currentCellWidth = currentInnerWidth + cardPadding*2 + 2
				innerWidth = currentInnerWidth
				rendered = append(rendered, renderGroupDivider(m.width, group.name, groupCounts[group.name]))
				currentGroup = group.name
			}
		}

		preview, ok := m.previews[session.ID]
		if !ok {
			continue
		}

		if preview.viewport.Width() != innerWidth {
			preview.viewport.SetWidth(innerWidth)
		}

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
			infoLines = cockpitCardInfoLines(innerWidth, m, session, pane, sessionState)
		}
		viewportHeight := bodyBudget
		if len(infoLines) > 0 {
			viewportHeight -= len(infoLines)
		}
		if viewportHeight < 0 {
			viewportHeight = 0
		}
		if preview.viewport.Height() != viewportHeight {
			preview.viewport.SetHeight(viewportHeight)
		}

		header := lipgloss.NewStyle().Render(formatHeader(innerWidth, session, window, pane, focused, pulsing, stale, cursor, sessionState, controls, m.hostname))
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
			state = sessionCockpitState(session, pane, stale)
		}
		if body != "" {
			body = compactFinishedBody(innerWidth, body, state, bodyBudget)
			body = compactOverviewBody(innerWidth, body, m.viewMode == viewModeOverview, bodyBudget)
		}
		switch {
		case state == "failed" || state == "route-fail" || state == "safety-fail":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitFail))
		case state == "blocked" || state == "review":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorBlocked))
		case state == "waiting":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorWaiting))
		case state == "done" || state == "pass" || state == "signal":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitOK))
		case state == "directional" || state == "null-safe":
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(headerColorCursor))
		case pane.Dead && pane.DeadStatus != 0:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitFail))
		case pane.Dead:
			borderStyle = borderStyle.BorderForeground(lipgloss.Color(borderColorExitOK))
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
		if len(currentRow) >= currentCols {
			flushRow()
		}

		m.cardLayout = append(m.cardLayout, cardBounds{
			sessionID:      session.ID,
			zoneID:         cardID,
			closeZoneID:    closeID,
			maximizeZoneID: maxID,
			collapseZoneID: collapseID,
		})
	}

	flushRow()

	return lipgloss.JoinVertical(lipgloss.Left, rendered...)
}

func (m *Model) cardLayoutForCount(count int) (int, int) {
	const (
		minInnerWidth   = 20
		columnOverhead  = cardPadding*2 + 2
		minColumnStride = minInnerWidth + columnOverhead
	)
	if count < 1 {
		count = 1
	}
	cols := 1
	if count > 1 {
		cols = min(count, max(1, (m.width+cardColumnGap)/(minColumnStride+cardColumnGap)))
		if m.preferredCols > 0 {
			cols = min(cols, m.preferredCols)
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
	totalBodyRows := 0
	type section struct {
		group cockpitGroup
		rows  int
		c     bodyHeightConstraint
	}
	sections := make([]section, 0, len(groups))
	for _, group := range groups {
		count := counts[group.name]
		if count <= 0 {
			continue
		}
		cols, _ := m.cardLayoutForCount(count)
		rows := (count + cols - 1) / cols
		if rows < 1 {
			rows = 1
		}
		fixedRows += rows * frameHeight
		c := bodyHeightConstraintForGroup(group)
		sections = append(sections, section{group: group, rows: rows, c: c})
		totalBodyRows += rows
	}
	bodyRows := available - fixedRows
	if bodyRows < totalBodyRows {
		bodyRows = max(totalBodyRows, bodyRows)
	}
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
		bestWeight := -1
		for i, section := range sections {
			height := heights[section.group.name]
			if section.c.max > 0 && height >= section.c.max {
				continue
			}
			cost := section.rows
			if cost <= 0 || cost > slack {
				continue
			}
			if section.c.weight > bestWeight {
				best = i
				bestWeight = section.c.weight
			}
		}
		if best < 0 {
			break
		}
		section := sections[best]
		heights[section.group.name]++
		slack -= section.rows
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

func bodyHeightConstraintForGroup(group cockpitGroup) bodyHeightConstraint {
	switch group.name {
	case groupNeedsInput.name, groupFailed.name:
		return bodyHeightConstraint{min: 12, max: 48, weight: 7}
	case groupRunningAgents.name, groupWork.name:
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

func renderGroupDivider(width int, label string, count int) string {
	if width < 1 {
		width = 1
	}
	text := label
	if count > 0 {
		text = fmt.Sprintf("%s  %d", label, count)
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("250")).
		Background(lipgloss.Color("236")).
		Bold(true).
		Padding(0, 1).
		Render(text)
}

// formatHeader builds the label line for a session card, colouring it based on
// status and focus state.
func formatHeader(width int, session tmux.Session, window tmux.Window, pane tmux.Pane, focused, pulsing, stale, cursor bool, attentionState string, controls string, host string) string {
	var meta []string
	hasDoneTiming := false
	if pane.Dead {
		meta = append(meta, pane.StatusString())
	}
	if !pane.LastActivity.IsZero() {
		meta = append(meta, fmt.Sprintf("last %s", coarseDuration(time.Since(pane.LastActivity))))
	}
	if pane.Cockpit != nil {
		if doneAt := parseCockpitTimestamp(pane.Cockpit.CompletedAt); !doneAt.IsZero() {
			meta = append(meta, fmt.Sprintf("done %s", coarseDuration(time.Since(doneAt))))
			hasDoneTiming = true
		} else if startedAt := parseCockpitTimestamp(pane.Cockpit.StartedAt); !startedAt.IsZero() {
			meta = append(meta, fmt.Sprintf("launched %s", coarseDuration(time.Since(startedAt))))
		}
	}
	state := attentionState
	if state == "" {
		state = cockpitState(pane, stale)
	}
	runtimeHeader := isOpenClawRuntimePane(pane)
	if state != "" && state != "running" && state != "starting" && state != "quiet" && !(state == "done" && hasDoneTiming) && !runtimeHeader {
		meta = append(meta, state)
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
	case state == "done" || state == "pass" || state == "signal":
		style = style.Foreground(lipgloss.Color(headerColorExitOK))
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
	return style.Render(header)
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
	if outcome := semanticPaneOutcome(pane); outcome.state != "" {
		return outcome.state
	}
	if pane.Dead && pane.DeadStatus != 0 {
		return "failed"
	}
	if pane.Cockpit != nil {
		if pane.Cockpit.DisplayOnly() && pane.Dead {
			return "done"
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

func sessionCockpitState(session tmux.Session, pane tmux.Pane, stale bool) string {
	if stale && isQuietLiveServiceSession(session) {
		stale = false
	}
	return cockpitState(pane, stale)
}

func cockpitInfoLines(width int, pane tmux.Pane) []string {
	if pane.Cockpit == nil {
		return nil
	}
	lines := []string{}
	goal := strings.TrimSpace(pane.Cockpit.Goal)
	if goal != "" {
		lines = append(lines, cockpitSubtleLine(width, "goal: "+goal))
	}
	if cleanup := cockpitCleanupLine(pane); cleanup != "" {
		lines = append(lines, cockpitSubtleLine(width, cleanup))
	}
	return lines
}

func cockpitCardInfoLines(width int, m *Model, session tmux.Session, pane tmux.Pane, sessionState string) []string {
	lines := []string{}
	if attention := cockpitAttentionLine(width, m, session, pane, sessionState); attention != "" {
		lines = append(lines, attention)
	}
	lines = append(lines, cockpitInfoLines(width, pane)...)
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

func compactFinishedBody(width int, body string, state string, maxLines int) string {
	switch state {
	case "done", "held", "stale", "failed", "route-fail", "safety-fail", "review", "pass", "signal", "directional", "null-safe":
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

func cockpitCleanupLine(pane tmux.Pane) string {
	if pane.Cockpit == nil {
		return ""
	}
	meta := pane.Cockpit
	parts := []string{}
	if policy := strings.TrimSpace(meta.CleanupPolicy); policy != "" {
		parts = append(parts, "policy: "+policy)
	}
	if ttl := strings.TrimSpace(meta.TTL); ttl != "" && ttl != "never" {
		parts = append(parts, "ttl: "+ttl)
	}
	if hold := strings.TrimSpace(meta.HoldReason); hold != "" {
		parts = append(parts, "hold: "+hold)
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
