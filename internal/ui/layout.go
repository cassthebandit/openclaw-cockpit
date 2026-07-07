// File layout.go contains helpers for sizing viewports and mapping mouse
// coordinates to cards.
package ui

import (
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

type innerDimension struct {
	width  int
	height int
}

// viewportFor builds a viewport with sane defaults for capturing pane output.
func viewportFor(dim innerDimension) viewport.Model {
	opts := []viewport.Option{}
	if dim.width > 0 {
		opts = append(opts, viewport.WithWidth(dim.width))
	}
	height := minPreviewHeight
	if dim.height > 0 {
		height = dim.height
	}
	opts = append(opts, viewport.WithHeight(height))

	vp := viewport.New(opts...)
	vp.MouseWheelEnabled = false
	vp.MouseWheelDelta = scrollStep
	return vp
}

// updatePreviewDimensions recalculates viewport sizes based on terminal
// geometry and how many sessions are visible.
func (m *Model) updatePreviewDimensions(count int) {
	if count <= 0 || m.width <= 0 || m.height <= 0 {
		return
	}
	offset := m.previewOffset
	if offset <= 0 || offset >= m.height {
		offset = topPaddingLines
	}
	footerHeight := max(1, m.footerHeight)
	availableHeight := m.height - offset - footerHeight
	if availableHeight < 1 {
		availableHeight = 1
	}
	const (
		frameHeight     = 3
		minInnerWidth   = 20
		columnOverhead  = cardPadding*2 + 2
		minColumnStride = minInnerWidth + columnOverhead
	)

	maxCols := 1
	if m.viewMode != viewModeDetail && count > 1 {
		maxCols = min(count, max(1, (m.width+cardColumnGap)/(minColumnStride+cardColumnGap)))
		if m.preferredCols > 0 {
			maxCols = min(maxCols, m.preferredCols)
		}
	}

	selectedCols := 1
	selectedHeight := 0
	selectedWidth := max(1, m.width-columnOverhead)

	for cols := maxCols; cols >= 1; cols-- {
		gapWidth := (cols - 1) * cardColumnGap
		usableWidth := m.width - gapWidth
		if usableWidth < cols {
			continue
		}
		columnWidth := usableWidth / cols
		if columnWidth < minColumnStride && cols > 1 {
			continue
		}
		innerWidth := columnWidth - columnOverhead
		if innerWidth < 1 {
			innerWidth = 1
		}
		rows := (count + cols - 1) / cols
		if rows < 1 {
			rows = 1
		}
		bodyBudget := availableHeight - rows*frameHeight
		if bodyBudget < 0 {
			bodyBudget = 0
		}
		cellBody := 0
		if rows > 0 {
			cellBody = bodyBudget / rows
		}
		candidateHeight := 0
		switch {
		case cellBody > 2:
			candidateHeight = cellBody - 2
		case cellBody > 0:
			candidateHeight = 1
		default:
			candidateHeight = 0
		}

		predicted := rows * (frameHeight + candidateHeight)
		if cols == maxCols {
			selectedCols = cols
			selectedWidth = innerWidth
			selectedHeight = candidateHeight
		}
		if predicted <= availableHeight {
			// distribute leftover height evenly if possible
			slack := availableHeight - predicted
			if slack > 0 && rows > 0 {
				candidateHeight += slack / rows
				if candidateHeight < 0 {
					candidateHeight = 0
				}
			}
			selectedCols = cols
			selectedWidth = innerWidth
			selectedHeight = candidateHeight
			break
		}
	}

	if selectedWidth < 1 {
		selectedWidth = 1
	}
	if selectedHeight < 0 {
		selectedHeight = 0
	}

	m.cardCols = selectedCols
	m.cardInnerWidth = selectedWidth
	m.cardInnerHeight = selectedHeight
	// Preview viewports are deliberately NOT resized here. renderSessionPreviews
	// is the single owner of per-card viewport dimensions (per-group widths and
	// per-card body budgets differ from these global values in organized mode).
	// Resizing here made AtBottom()/autoFollow decisions in paneContentMsg run
	// against a height the card was never rendered with, which broke preview
	// scroll anchoring during interaction.
}

// pageScrollBy moves the whole-wall offset by delta lines, clamped to the
// engaged range. It no-ops (returns false) when page scroll is not engaged.
func (m *Model) pageScrollBy(delta int) bool {
	if !m.pageScrollEngaged {
		return false
	}
	before := m.pageOffset
	m.pageOffset += delta
	if m.pageOffset < 0 {
		m.pageOffset = 0
	}
	if m.pageOffset > m.pageMaxOffset {
		m.pageOffset = m.pageMaxOffset
	}
	return m.pageOffset != before
}

// pageStep is the line delta for a PgUp/PgDn on the wall.
func (m *Model) pageStep() int {
	if m.pageContentHeight > 1 {
		return m.pageContentHeight - 1
	}
	return 1
}

// scrollCursorIntoView nudges the whole-wall offset so the cursor's card is
// visible. It uses the previous render's line map (one-frame approximation) and
// only acts while page scroll is engaged.
func (m *Model) scrollCursorIntoView() {
	if !m.pageScrollEngaged {
		return
	}
	top, ok := m.cardTopLine[m.cursorSession]
	if !ok {
		return
	}
	height := m.cardLineHeight[m.cursorSession]
	if height < 1 {
		height = 1
	}
	if m.pageContentHeight < 1 {
		return
	}
	if top < m.pageOffset {
		m.pageOffset = top
	} else if top+height > m.pageOffset+m.pageContentHeight {
		m.pageOffset = top + height - m.pageContentHeight
	}
	if m.pageOffset < 0 {
		m.pageOffset = 0
	}
	if m.pageOffset > m.pageMaxOffset {
		m.pageOffset = m.pageMaxOffset
	}
}

// cardAt resolves the card located at the given mouse coordinates. Zones are
// checked first; when whole-wall windowing clipped one of a card's two zone
// markers (a card straddling the scroll window edge), the zone does not exist
// for this frame, so render-time geometry is used as a fallback. Without the
// fallback, partially visible cards are unclickable and un-hoverable.
func (m *Model) cardAt(msg tea.MouseMsg) (cardBounds, bool) {
	for _, card := range m.cardLayout {
		if info := zone.Get(card.zoneID); info != nil && info.InBounds(msg) {
			return card, true
		}
	}
	mouse := msg.Mouse()
	for _, card := range m.cardLayout {
		if m.cardGeometryContains(card, mouse.X, mouse.Y) {
			return card, true
		}
	}
	return cardBounds{}, false
}

// cardGeometryContains reports whether the screen coordinate falls inside the
// card's render-time geometry, mapping screen lines back to grid lines through
// the whole-wall scroll state of the same frame (windowGrid reserves one top
// indicator line when engaged).
func (m *Model) cardGeometryContains(card cardBounds, x, y int) bool {
	if !card.hasGeometry || card.gridHeight <= 0 {
		return false
	}
	if x < card.screenX0 || x > card.screenX1 {
		return false
	}
	gridY := 0
	if m.pageScrollEngaged {
		if y <= m.previewOffset || y > m.previewOffset+m.pageContentHeight {
			return false
		}
		gridY = y - m.previewOffset - 1 + m.pageOffset
	} else {
		if y < m.previewOffset || y >= m.previewOffset+m.pageContentHeight {
			return false
		}
		gridY = y - m.previewOffset
	}
	return gridY >= card.gridTop && gridY < card.gridTop+card.gridHeight
}

// groupAt resolves the accordion group divider located at the given mouse
// coordinates, if any.
func (m *Model) groupAt(msg tea.MouseMsg) (string, bool) {
	for _, gz := range m.groupZones {
		if info := zone.Get(gz.zoneID); info != nil && info.InBounds(msg) {
			return gz.name, true
		}
	}
	return "", false
}

// cursorGroupName returns the accordion group name of the session under the
// grid cursor, or "" when there is no cursor.
func (m *Model) cursorGroupName() string {
	if m.cursorSession == "" {
		return ""
	}
	session, ok := m.sessionByID(m.cursorSession)
	if !ok {
		return ""
	}
	return cockpitGroupFor(m, session).name
}

// ensureCursor keeps the cursor pointing at a visible session entry.
func (m *Model) ensureCursor(sessions []tmux.Session) {
	if len(sessions) == 0 {
		m.cursorSession = ""
		return
	}
	if m.cursorSession == "" {
		m.cursorSession = sessions[0].ID
		return
	}
	for _, session := range sessions {
		if session.ID == m.cursorSession {
			return
		}
	}
	m.cursorSession = sessions[0].ID
}

// moveCursorLeft shifts the cursor one column to the left when possible.
func (m *Model) moveCursorLeft() bool {
	return m.moveCursorByDelta(-1, true)
}

// moveCursorRight shifts the cursor one column to the right when possible.
func (m *Model) moveCursorRight() bool {
	return m.moveCursorByDelta(1, true)
}

// moveCursorUp moves the cursor up one row in the card grid.
func (m *Model) moveCursorUp() bool {
	cols := max(1, m.cardCols)
	return m.moveCursorByDelta(-cols, false)
}

// moveCursorDown moves the cursor down one row in the card grid.
func (m *Model) moveCursorDown() bool {
	cols := max(1, m.cardCols)
	return m.moveCursorByDelta(cols, false)
}

// moveCursorByDelta advances the cursor by the provided delta if permitted.
func (m *Model) moveCursorByDelta(delta int, enforceRow bool) bool {
	sessions := m.gridSessions()
	if len(sessions) == 0 {
		m.cursorSession = ""
		return false
	}
	m.ensureCursor(sessions)
	cols := max(1, m.cardCols)
	currentIndex := -1
	for idx, session := range sessions {
		if session.ID == m.cursorSession {
			currentIndex = idx
			break
		}
	}
	if currentIndex == -1 {
		return false
	}
	nextIndex := currentIndex + delta
	if nextIndex < 0 || nextIndex >= len(sessions) {
		return false
	}
	if enforceRow {
		currentRow := currentIndex / cols
		nextRow := nextIndex / cols
		if currentRow != nextRow {
			return false
		}
	}
	m.cursorSession = sessions[nextIndex].ID
	return true
}
