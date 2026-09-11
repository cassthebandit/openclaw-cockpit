// File handlers.go groups input handling routines for keyboard and mouse
// events.
package ui

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// handleGlobalKey processes keys that apply regardless of focus.
func (m *Model) handleGlobalKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); !ok {
		return false, nil
	}
	if press, ok := msg.(tea.KeyPressMsg); ok {
		switch press.Key().Text {
		case ":", "v":
			m.resetCtrlC()
			m.commanding = true
			m.commandInput.SetValue("")
			m.commandInput.CursorEnd()
			m.commandInput.Focus()
			return true, nil
		case "d":
			return m.enterDetailFromKeyboard()
		}
	}
	if msg.String() != "esc" {
		m.lastEsc = time.Time{}
	}
	switch msg.String() {
	case "shift+left":
		m.shiftActiveTab(-1)
		m.updatePreviewDimensions(m.filteredSessionCount())
		return true, nil
	case "shift+right":
		m.shiftActiveTab(1)
		m.updatePreviewDimensions(m.filteredSessionCount())
		return true, nil
	case "left":
		if m.focusedSession != "" {
			return false, nil
		}
		if m.moveCursorLeft() {
			return true, nil
		}
		return true, nil
	case "right":
		if m.focusedSession != "" {
			return false, nil
		}
		if m.moveCursorRight() {
			return true, nil
		}
		return true, nil
	case "up":
		if m.focusedSession != "" {
			return false, nil
		}
		m.moveCursorUp()
		m.scrollCursorIntoView()
		return true, nil
	case "down":
		if m.focusedSession != "" {
			return false, nil
		}
		m.moveCursorDown()
		m.scrollCursorIntoView()
		return true, nil
	case "pgup":
		// Focused: the card owns the key (handled by handleFocusedKey).
		// Unfocused: page the whole wall.
		if m.focusedSession != "" {
			return false, nil
		}
		m.pageScrollBy(-m.pageStep())
		return true, nil
	case "pgdown":
		if m.focusedSession != "" {
			return false, nil
		}
		m.pageScrollBy(m.pageStep())
		return true, nil
	case "enter":
		if m.cursorSession == "" {
			return true, nil
		}
		if m.focusedSession != m.cursorSession {
			m.focusedSession = m.cursorSession
			m.resetCtrlC()
			m.scrollCursorIntoView() // reveal a card focused from below the fold
			if preview, ok := m.previews[m.focusedSession]; ok {
				preview.viewport.GotoBottom()
				preview.autoFollow = true
				if preview.paneID != "" {
					return true, fetchPaneVarsCmd(m.client, m.focusedSession, preview.paneID)
				}
			}
		}
		return true, nil
	case "/", "ctrl+f":
		m.resetCtrlC()
		m.searching = true
		m.searchInput.SetValue(m.searchQuery)
		m.searchInput.CursorEnd()
		m.searchInput.Focus()
		return true, nil
	case ":", "shift+;", "v":
		m.resetCtrlC()
		m.commanding = true
		m.commandInput.SetValue("")
		m.commandInput.CursorEnd()
		m.commandInput.Focus()
		return true, nil
	case "esc":
		if m.viewMode == viewModeDetail && m.activeTab == 1 {
			m.leaveDetail(false)
			m.updatePreviewDimensions(m.filteredSessionCount())
			return true, nil
		}
		if m.searchQuery != "" {
			m.resetCtrlC()
			m.searchQuery = ""
			m.updatePreviewDimensions(m.filteredSessionCount())
			return true, nil
		}
		if m.viewFilter != "" {
			m.resetCtrlC()
			m.viewFilter = ""
			m.updatePreviewDimensions(m.filteredSessionCount())
			return true, nil
		}
		if m.focusedSession == "" {
			return false, nil
		}
		now := m.clockNow()
		if !m.lastEsc.IsZero() && now.Sub(m.lastEsc) < quitChordWindow {
			previous := m.focusedSession
			m.focusedSession = ""
			m.cursorSession = previous
			m.lastEsc = time.Time{}
			m.resetCtrlC()
			return true, nil
		}
		m.lastEsc = now
		return true, nil
	case "ctrl+p":
		if m.paletteOpen {
			m.closePalette()
		} else {
			m.openCommandPalette()
		}
		return true, nil
	case "d":
		return m.enterDetailFromKeyboard()
	case "H":
		if len(m.hidden) > 0 {
			m.resetCtrlC()
			m.hidden = make(map[string]struct{})
			m.updatePreviewDimensions(m.filteredSessionCount())
		}
		return true, nil
	case "c":
		// Toggle the accordion group under the cursor. When a card is focused
		// the key belongs to the pane (focus wins), so defer to the focused
		// handler instead of collapsing.
		if m.focusedSession != "" || !m.organized || m.viewMode != viewModeOverview {
			return false, nil
		}
		if name := m.cursorGroupName(); name != "" {
			m.resetCtrlC()
			m.toggleGroupCollapsed(name)
			m.updatePreviewDimensions(m.filteredSessionCount())
		}
		return true, nil
	case "C":
		if m.focusedSession != "" || !m.organized || m.viewMode != viewModeOverview {
			return false, nil
		}
		m.resetCtrlC()
		groups := orderedCockpitGroups(m, m.filteredSessions())
		m.setAllGroupsCollapsed(groups, m.anyGroupExpanded(groups))
		m.updatePreviewDimensions(m.filteredSessionCount())
		return true, nil
	case "ctrl+x":
		m.resetCtrlC()
		return true, showStatusMessage("cleanup disabled: use session_hygiene.py or safe_kill.py")
	case "q":
		m.resetCtrlC()
		return true, tea.Quit
	case "X":
		if m.focusedSession == "" {
			return true, nil
		}
		m.resetCtrlC()
		return true, showStatusMessage("cleanup disabled: use session_hygiene.py or safe_kill.py")
	}
	return false, nil
}

func (m *Model) enterDetailFromKeyboard() (bool, tea.Cmd) {
	target := m.focusedSession
	if target == "" {
		target = m.cursorSession
	}
	if target != "" {
		m.handleDetailToggle(target)
		m.updatePreviewDimensions(m.filteredSessionCount())
	}
	return true, nil
}

// handleFocusedKey forwards navigation and control keys to the focused pane or
// local viewport.
func (m *Model) handleFocusedKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); !ok {
		return false, nil
	}
	if m.focusedSession == "" {
		return false, nil
	}
	preview, ok := m.previews[m.focusedSession]
	if !ok {
		return false, nil
	}
	pane, paneOK := m.paneFor(m.focusedSession)
	switch msg.String() {
	case "up":
		m.resetCtrlC()
		preview.viewport.ScrollUp(1)
		preview.autoFollow = false
		return true, nil
	case "down":
		m.resetCtrlC()
		preview.viewport.ScrollDown(1)
		preview.autoFollow = preview.viewport.AtBottom()
		return true, nil
	case "pgup":
		m.resetCtrlC()
		preview.viewport.PageUp()
		preview.autoFollow = false
		return true, nil
	case "pgdown":
		m.resetCtrlC()
		preview.viewport.PageDown()
		preview.autoFollow = preview.viewport.AtBottom()
		return true, nil
	case "ctrl+u":
		m.resetCtrlC()
		preview.viewport.ScrollUp(scrollStep)
		preview.autoFollow = false
		return true, nil
	case "ctrl+d":
		m.resetCtrlC()
		preview.viewport.ScrollDown(scrollStep)
		preview.autoFollow = preview.viewport.AtBottom()
		return true, nil
	case "g":
		m.resetCtrlC()
		preview.viewport.GotoTop()
		preview.autoFollow = false
		return true, nil
	case "G":
		m.resetCtrlC()
		preview.viewport.GotoBottom()
		preview.autoFollow = true
		return true, nil
	case "ctrl+c":
		if m.monitorOnly {
			m.resetCtrlC()
			return true, tea.Quit
		}
		if !paneOK || pane.Dead || preview.paneID == "" {
			return true, tea.Quit
		}
		// First press: forward exactly one C-c and arm the quit chord.
		// Second press inside the window: quit Cockpit WITHOUT forwarding a
		// second interrupt to the pane.
		now := m.clockNow()
		if !m.lastCtrlC.IsZero() && now.Sub(m.lastCtrlC) < quitChordWindow {
			m.resetCtrlC()
			return true, tea.Quit
		}
		m.lastCtrlC = now
		return true, sendKeysCmd(m.client, preview.paneID, "C-c")
	case "ctrl+m":
		target := m.focusedSession
		if target == "" {
			target = m.cursorSession
		}
		if target != "" {
			m.handleDetailToggle(target)
			m.updatePreviewDimensions(m.filteredSessionCount())
		}
		return true, nil
	case "z":
		if m.focusedSession != "" {
			m.toggleCollapsed(m.focusedSession)
			m.updatePreviewDimensions(m.filteredSessionCount())
			return true, nil
		}
	case "Z":
		m.clearCollapsed()
		m.updatePreviewDimensions(m.filteredSessionCount())
		return true, nil
	}

	input, ok := tmuxKeysFrom(msg)
	if !ok || preview.paneID == "" {
		m.resetCtrlC()
		return false, nil
	}
	if m.monitorOnly {
		m.resetCtrlC()
		return true, showStatusMessage("monitor-only: key forwarding disabled")
	}
	m.resetCtrlC()
	if input.literal {
		return true, sendLiteralKeysCmd(m.client, preview.paneID, input.text)
	}
	return true, sendKeysCmd(m.client, preview.paneID, input.keys...)
}

// tmuxKeyInput separates the two send-keys grammars: named tmux key tokens
// (Enter, arrows, C-c, ...) versus printable text that must be forwarded with
// literal semantics so it can never be parsed as key names.
type tmuxKeyInput struct {
	keys    []string
	text    string
	literal bool
}

// tmuxKeysFrom converts Bubble Tea key messages into tmux key input. Named
// special keys map to key tokens; printable input (including space) maps to
// literal text. Ordinary keys reserved by the dashboard never reach this
// function — control mode is deliberately partial, not a transparent
// terminal.
func tmuxKeysFrom(msg tea.KeyMsg) (tmuxKeyInput, bool) {
	press, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return tmuxKeyInput{}, false
	}
	key := press.Key()
	switch key.Code {
	case tea.KeyEnter:
		return tmuxKeyInput{keys: []string{"Enter"}}, true
	case tea.KeyTab:
		return tmuxKeyInput{keys: []string{"Tab"}}, true
	case tea.KeySpace:
		return tmuxKeyInput{text: " ", literal: true}, true
	case tea.KeyBackspace:
		return tmuxKeyInput{keys: []string{"BSpace"}}, true
	case tea.KeyDelete:
		return tmuxKeyInput{keys: []string{"Delete"}}, true
	case tea.KeyEsc:
		return tmuxKeyInput{keys: []string{"Escape"}}, true
	case tea.KeyUp:
		return tmuxKeyInput{keys: []string{"Up"}}, true
	case tea.KeyDown:
		return tmuxKeyInput{keys: []string{"Down"}}, true
	case tea.KeyLeft:
		return tmuxKeyInput{keys: []string{"Left"}}, true
	case tea.KeyRight:
		return tmuxKeyInput{keys: []string{"Right"}}, true
	}
	if key.Mod&tea.ModAlt != 0 {
		return tmuxKeyInput{}, false
	}
	if key.Text == "" {
		return tmuxKeyInput{}, false
	}
	return tmuxKeyInput{text: key.Text, literal: true}, true
}

// handleMouse wires up focus toggles, pane hiding, and scroll gestures.
func (m *Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	if handled, cmd := m.handleTabMouse(msg); handled {
		return m, cmd
	}
	// An accordion divider click toggles that group. Checked before the card
	// layout guard so it still works when every group is collapsed.
	if name, ok := m.groupDividerClick(msg); ok {
		m.toggleGroupCollapsed(name)
		m.updatePreviewDimensions(m.filteredSessionCount())
		return m, nil
	}
	if len(m.cardLayout) == 0 {
		if _, motion := msg.(tea.MouseMotionMsg); motion {
			m.hoveredSession = ""
			m.hoveredControl = ""
			return m, nil
		}
		m.wheelScrollWall(msg)
		return m, nil
	}
	card, ok := m.cardAt(msg)
	m.logMouseEvent(msg, card, ok)
	if !ok {
		if _, motion := msg.(tea.MouseMotionMsg); motion {
			m.hoveredSession = ""
			m.hoveredControl = ""
			return m, nil
		}
		// Wheel over a divider/gutter/empty area scrolls the whole wall.
		m.wheelScrollWall(msg)
		return m, nil
	}
	if _, motion := msg.(tea.MouseMotionMsg); motion {
		m.hoveredSession = card.sessionID
		m.hoveredControl = controlUnderPointer(card, msg)
		return m, nil
	}
	preview := m.previews[card.sessionID]
	mouse := msg.Mouse()
	switch mouse.Button {
	case tea.MouseWheelDown:
		if _, wheel := msg.(tea.MouseWheelMsg); wheel {
			// Scroll the card if it can still scroll down; otherwise (no
			// overflow or already at the bottom) bubble to the wall.
			if m.cardOwnsWheel(card.sessionID) && preview != nil && !preview.viewport.AtBottom() {
				preview.viewport.ScrollDown(scrollStep)
				preview.autoFollow = preview.viewport.AtBottom()
				m.hoveredSession = card.sessionID
			} else {
				m.pageScrollBy(scrollStep)
			}
		}
	case tea.MouseWheelUp:
		if _, wheel := msg.(tea.MouseWheelMsg); wheel {
			if m.cardOwnsWheel(card.sessionID) && preview != nil && !preview.viewport.AtTop() {
				preview.viewport.ScrollUp(scrollStep)
				preview.autoFollow = false
				m.hoveredSession = card.sessionID
			} else {
				m.pageScrollBy(-scrollStep)
			}
		}
	case tea.MouseLeft:
		if _, click := msg.(tea.MouseClickMsg); click {
			if info := zone.Get(card.maximizeZoneID); info != nil && info.InBounds(msg) {
				m.hoveredControl = ""
				m.handleDetailToggle(card.sessionID)
				m.updatePreviewDimensions(m.filteredSessionCount())
				return m, nil
			}
			if card.collapseZoneID != "" {
				if info := zone.Get(card.collapseZoneID); info != nil && info.InBounds(msg) {
					m.hoveredControl = ""
					m.toggleCollapsed(card.sessionID)
					m.updatePreviewDimensions(m.filteredSessionCount())
					return m, nil
				}
			}
			if info := zone.Get(card.closeZoneID); info != nil && info.InBounds(msg) {
				m.hidden[card.sessionID] = struct{}{}
				if m.focusedSession == card.sessionID {
					m.focusedSession = ""
				}
				if m.cursorSession == card.sessionID {
					m.cursorSession = ""
				}
				if m.hoveredSession == card.sessionID {
					m.hoveredSession = ""
				}
				m.hoveredControl = ""
				delete(m.previews, card.sessionID)
				m.resetCtrlC()
				m.updatePreviewDimensions(m.filteredSessionCount())
				return m, showStatusMessage(fmt.Sprintf("Hidden in Cockpit: %s", sessionLabel(card.sessionID)))
			}
			m.focusedSession = card.sessionID
			m.cursorSession = card.sessionID
			m.hoveredSession = card.sessionID
			m.resetCtrlC()
			m.scrollCursorIntoView()
			if preview != nil {
				preview.viewport.GotoBottom()
				preview.autoFollow = true
				if preview.paneID != "" {
					return m, fetchPaneVarsCmd(m.client, card.sessionID, preview.paneID)
				}
			}
		}
	}
	return m, nil
}

func (m *Model) cardOwnsWheel(sessionID string) bool {
	return sessionID != "" && m.viewMode == viewModeDetail && m.detailSession == sessionID
}

// handleTabMouse reacts to mouse input overlapping the tab strip and switches
// tabs when the user clicks the corresponding title.
func (m *Model) handleTabMouse(msg tea.MouseMsg) (bool, tea.Cmd) {
	mouse := msg.Mouse()
	if mouse.Button != tea.MouseLeft {
		return false, nil
	}
	switch msg.(type) {
	case tea.MouseWheelMsg, tea.MouseMotionMsg:
		return false, nil
	}
	manager := zone.DefaultManager
	if manager == nil {
		return false, nil
	}
	ids := manager.IDsInBounds(msg)
	if len(ids) == 0 {
		return false, nil
	}
	tabIndex, ok := tabIndexFromZoneIDs(ids)
	if !ok {
		return false, nil
	}
	switch msg.(type) {
	case tea.MouseClickMsg, tea.MouseReleaseMsg:
		m.setActiveTab(tabIndex)
		m.updatePreviewDimensions(m.filteredSessionCount())
		return true, nil
	}
	return false, nil
}

// tabIndexFromZoneIDs inspects zone identifiers and extracts the tab index
// encoded by BubbleApp's `tabtitles` component.
func tabIndexFromZoneIDs(ids []string) (int, bool) {
	for _, rawID := range ids {
		child := rawID
		if idx := strings.LastIndex(rawID, "###"); idx >= 0 {
			child = rawID[idx+3:]
		}
		if !strings.HasPrefix(child, "tab:") {
			continue
		}
		parts := strings.SplitN(child, ":", 2)
		if len(parts) != 2 {
			continue
		}
		tabIndex, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		return tabIndex, true
	}
	return 0, false
}

// wheelScrollWall applies a mouse-wheel event to the whole-wall page offset.
func (m *Model) wheelScrollWall(msg tea.MouseMsg) {
	if _, wheel := msg.(tea.MouseWheelMsg); !wheel {
		return
	}
	switch msg.Mouse().Button {
	case tea.MouseWheelDown:
		m.pageScrollBy(scrollStep)
	case tea.MouseWheelUp:
		m.pageScrollBy(-scrollStep)
	}
}

// groupDividerClick reports whether the event is a left-click on an accordion
// divider, returning the group name to toggle.
func (m *Model) groupDividerClick(msg tea.MouseMsg) (string, bool) {
	if _, click := msg.(tea.MouseClickMsg); !click {
		return "", false
	}
	if msg.Mouse().Button != tea.MouseLeft {
		return "", false
	}
	return m.groupAt(msg)
}

func controlUnderPointer(card cardBounds, msg tea.MouseMsg) string {
	for _, id := range []string{card.maximizeZoneID, card.collapseZoneID, card.closeZoneID} {
		if info := zone.Get(id); info != nil && info.InBounds(msg) {
			return id
		}
	}
	return ""
}

// handleSearchKey updates the search field when the user is actively editing.
func (m *Model) handleSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); !ok {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.searching = false
		m.searchInput.Blur()
		return m, nil
	case "enter":
		m.searchQuery = strings.TrimSpace(m.searchInput.Value())
		m.searching = false
		m.searchInput.Blur()
		m.updatePreviewDimensions(m.filteredSessionCount())
		return m, nil
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.searchInput, cmd = m.searchInput.Update(msg)
	m.searchQuery = strings.TrimSpace(m.searchInput.Value())
	m.updatePreviewDimensions(m.filteredSessionCount())
	return m, cmd
}

// handleCommandKey updates the Pulse command filter when the user is editing
// the ':' prompt.
func (m *Model) handleCommandKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); !ok {
		return m, nil
	}
	switch msg.String() {
	case "esc":
		m.commanding = false
		m.commandInput.Blur()
		return m, nil
	case "enter":
		command := strings.TrimSpace(strings.TrimPrefix(m.commandInput.Value(), ":"))
		m.commanding = false
		m.commandInput.Blur()
		if command == "" {
			return m, nil
		}
		if m.applyViewFilterCommand(command) {
			m.updatePreviewDimensions(m.filteredSessionCount())
			return m, nil
		}
		return m, showStatusMessage(fmt.Sprintf("unknown filter :%s", command))
	case "ctrl+c":
		return m, tea.Quit
	}
	var cmd tea.Cmd
	m.commandInput, cmd = m.commandInput.Update(msg)
	return m, cmd
}

func (m *Model) applyViewFilterCommand(command string) bool {
	switch strings.ToLower(strings.TrimSpace(command)) {
	case "pulse", "all":
		m.viewFilter = ""
	case "decision", "decisions":
		m.viewFilter = "decision"
	case "route", "routes":
		m.viewFilter = "route"
	case "handoff", "handoffs", "delivery":
		m.viewFilter = "handoff"
	case "services", "service":
		m.viewFilter = "services"
	default:
		return false
	}
	return true
}

// isHidden reports whether the given session ID is hidden from the grid.
func (m *Model) isHidden(id string) bool {
	_, ok := m.hidden[id]
	return ok
}

// resetCtrlC clears the timing cache used to detect the quit chord.
func (m *Model) resetCtrlC() {
	m.lastCtrlC = time.Time{}
}

// paneFor resolves the active pane for a session, returning false when the
// session has no panes.
func (m *Model) paneFor(sessionID string) (tmux.Pane, bool) {
	for _, session := range m.sessions {
		if session.ID == sessionID {
			window, ok := activeWindow(session)
			if !ok {
				return tmux.Pane{}, false
			}
			return activePane(window)
		}
	}
	return tmux.Pane{}, false
}
