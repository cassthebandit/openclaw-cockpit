// File view.go orchestrates the final Bubble Tea view composition.
package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"
)

const (
	topPaddingLines = 0
	// maxFooterHeight bounds the status footer so helper/status text can never
	// crowd card content out of the viewport (layout contract: the footer must
	// stay compact and never steal meaningful active-agent height).
	maxFooterHeight = 4
)

// View renders the entire OpenClaw Cockpit interface, including title bar, search
// state, session previews, status footer, and overlays.
//
// F1 frame cache: when no render-affecting state change has been marked since
// the last build and the clock has not crossed the next scheduled time-based
// transition, the previously composed (and zone-scanned) frame is returned
// as-is. Skipping zone.Scan on cached frames is safe because the global zone
// manager retains the last scan's hit boxes: nothing in this package clears
// them outside Scan itself.
func (m *Model) View() tea.View {
	if m.width == 0 || m.height == 0 {
		return tea.NewView("loading...")
	}

	now := m.clockNow()
	if m.cachedViewOK && !m.renderDirty && (m.nextRenderAt.IsZero() || now.Before(m.nextRenderAt)) {
		return m.cachedView
	}

	targetWidth := max(m.width, 1)
	targetHeight := max(m.height, 1)

	m.setActiveTab(m.activeTab)
	headerParts := []string{renderTitleBar(m, targetWidth)}
	if m.commanding {
		headerParts = append(headerParts, renderCommandBar(m.commandInput))
	} else if m.searching {
		headerParts = append(headerParts, renderSearchBar(m.searchInput))
	} else if m.searchQuery != "" {
		headerParts = append(headerParts, renderSearchSummary(m.searchQuery))
	}

	header := lipgloss.JoinVertical(lipgloss.Left, headerParts...)
	headerHeight := max(1, countLines(header))
	m.previewOffset = headerHeight

	footerCap := m.footerMaxHeight
	if footerCap < 1 {
		footerCap = maxFooterHeight
	}
	status := m.renderStatus()
	m.footerHeight = max(1, min(countLines(status), footerCap))
	status = clampFooter(status, m.footerHeight)
	// Compute the filtered/sorted wall once per frame; layout sizing, card
	// rendering, and cursor selection all reuse this slice instead of
	// re-filtering and re-sorting per call.
	visible := m.filteredSessions()
	m.updatePreviewDimensions(len(visible))

	availableHeight := max(0, targetHeight-headerHeight-m.footerHeight)
	gridContent := m.renderSessionCards(visible)
	if gridContent == "" {
		m.resetPageScroll()
		gridContent = emptyStateView(targetWidth, availableHeight)
	} else {
		gridContent = m.windowGrid(gridContent, availableHeight)
		gridContent = placeGridContent(gridContent, targetWidth, availableHeight)
	}

	footerView := status
	if m.footer != nil {
		m.footer.SetWidth(targetWidth)
		m.footer.SetHeight(m.footerHeight)
		m.footer.SetContent(status)
		footerView = m.footer.View()
	}

	segments := []string{header, gridContent}
	segments = append(segments, footerView)
	view := lipgloss.JoinVertical(lipgloss.Left, segments...)
	view = lipgloss.Place(targetWidth, targetHeight, lipgloss.Left, lipgloss.Top, view)

	if m.traceMouse {
		m.logCardLayout()
	}

	if m.paletteOpen {
		palette := m.renderCommandPalette()
		paletteWidth := lipgloss.Width(palette)
		paletteHeight := countLines(palette)
		width := max(m.width, max(lipgloss.Width(view), paletteWidth))
		height := max(m.height, max(countLines(view), paletteHeight))
		offsetX := max((width-paletteWidth)/2, 0)
		offsetY := max((height-paletteHeight)/2, 0)

		view = overlayView(view, palette, width, height, offsetX, offsetY)
	}

	content := tea.NewView(zone.Scan(view))
	content.AltScreen = true
	content.MouseMode = tea.MouseModeCellMotion

	m.cachedView = content
	m.cachedViewOK = true
	m.renderDirty = false
	m.renderBuilds++
	// A rebuild can consume a passed time transition on a clean message (the
	// armed tick may still be in flight); recompute so the cache is not
	// treated as permanently expired. Ticks are armed only from Update.
	m.nextRenderAt = m.computeNextRenderTransition(now)
	return content
}

// clampFooter bounds the footer to limit lines by dropping lines from the TOP:
// the key-hint helper line is the most expendable, while stale warnings,
// janitor health, toasts, and errors accumulate at the tail and must survive.
func clampFooter(status string, limit int) string {
	if limit <= 0 {
		return ""
	}
	lines := strings.Split(status, "\n")
	if len(lines) <= limit {
		return status
	}
	kept := append([]string(nil), lines[len(lines)-limit:]...)
	for _, line := range kept {
		if strings.Contains(line, "janitor:") {
			return strings.Join(kept, "\n")
		}
	}
	for _, line := range lines[:len(lines)-limit] {
		if strings.Contains(line, "janitor:") {
			kept[0] = line
			break
		}
	}
	return strings.Join(kept, "\n")
}

// resetPageScroll disengages whole-wall scroll (used when there is no grid).
func (m *Model) resetPageScroll() {
	m.pageOffset = 0
	m.pageScrollEngaged = false
	m.pageMaxOffset = 0
	m.pageContentHeight = 0
}

// windowGrid gates and applies whole-wall scroll. It engages only when the
// CURRENT rendered grid exceeds the available height (after accordion state,
// filters, terminal size, and view mode are baked into `content`); otherwise the
// offset is pinned to 0 and no indicators show. When engaged it reserves one top
// and one bottom line for "N more" indicators and returns exactly `height` lines.
func (m *Model) windowGrid(content string, height int) string {
	if height <= 0 {
		m.resetPageScroll()
		return ""
	}
	lines := strings.Split(content, "\n")
	total := len(lines)
	if total <= height {
		// Fits: no scroll, offset pinned to 0, indicators hidden.
		m.pageOffset = 0
		m.pageScrollEngaged = false
		m.pageMaxOffset = 0
		m.pageContentHeight = total
		return content
	}

	contentHeight := max(1, height-2) // reserve top + bottom indicator lines
	maxOffset := max(0, total-contentHeight)
	if m.pageOffset < 0 {
		m.pageOffset = 0
	}
	if m.pageOffset > maxOffset {
		m.pageOffset = maxOffset
	}
	m.pageScrollEngaged = true
	m.pageMaxOffset = maxOffset
	m.pageContentHeight = contentHeight

	offset := m.pageOffset
	end := min(total, offset+contentHeight)
	window := append([]string{scrollIndicator(m.width, "▲", offset)}, lines[offset:end]...)
	window = append(window, scrollIndicator(m.width, "▼", total-end))
	return strings.Join(window, "\n")
}

// scrollIndicator renders a centered "▲/▼ N more" hint, or a blank spacer line
// when there is nothing more in that direction.
func scrollIndicator(width int, arrow string, count int) string {
	if width < 1 {
		width = 1
	}
	text := ""
	if count > 0 {
		text = fmt.Sprintf("%s %d more", arrow, count)
	}
	return lipgloss.NewStyle().
		Width(width).
		Foreground(lipgloss.Color("244")).
		Align(lipgloss.Center).
		Render(text)
}

func clampHeight(content string, limit int) string {
	if limit <= 0 || content == "" {
		return ""
	}

	consumed := 0
	lines := 0
	for consumed < len(content) && lines < limit {
		remainder := content[consumed:]
		idx := strings.IndexByte(remainder, '\n')
		if idx == -1 {
			return content
		}
		consumed += idx + 1
		lines++
	}

	if lines < limit {
		return content
	}
	if consumed > 0 && content[consumed-1] == '\n' {
		consumed--
	}
	return content[:consumed]
}

func emptyStateView(width, height int) string {
	if width <= 0 {
		width = 40
	}
	message := "No tmux sessions detected."
	helper := "Start one with `tmux new -s demo`."
	box := lipgloss.JoinVertical(lipgloss.Left, message, helper)
	styled := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color(borderColorCursor)).
		Padding(1, 2).
		Foreground(lipgloss.Color("252")).
		Render(box)

	maxWidth := max(width, lipgloss.Width(styled))
	if height <= 0 {
		return lipgloss.PlaceHorizontal(maxWidth, lipgloss.Center, styled)
	}
	maxHeight := max(height, countLines(styled))
	return lipgloss.Place(maxWidth, maxHeight, lipgloss.Center, lipgloss.Center, styled)
}

func placeGridContent(content string, width, height int) string {
	if height <= 0 {
		return ""
	}
	if width <= 0 {
		width = 1
	}
	return lipgloss.Place(width, height, lipgloss.Left, lipgloss.Top, content, lipgloss.WithWhitespaceChars(" "))
}
