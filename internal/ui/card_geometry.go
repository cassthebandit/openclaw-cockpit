package ui

import "github.com/cassthebandit/openclaw-cockpit/internal/tmux"

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
		groupMarkedForTeardown.name,
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
		// A group consisting entirely of alternate-screen panes cannot use
		// more rows than its tallest native screen plus card information.
		capHeight, allNative := 0, true
		for _, session := range sessions {
			if cockpitGroupFor(m, session).name != group.name {
				continue
			}
			for _, window := range session.Windows {
				for _, pane := range window.Panes {
					if !pane.AlternateScreen || pane.Height <= 0 {
						allNative = false
					}
					capHeight = max(capHeight, pane.Height+6)
				}
			}
		}
		if allNative && capHeight > 0 {
			c.max = min(c.max, capHeight)
			c.min = min(c.min, c.max)
		}
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
	case groupMarkedForTeardown.name:
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
	case groupCompletedAgents.name:
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
