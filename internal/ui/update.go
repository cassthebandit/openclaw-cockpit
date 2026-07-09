// File update.go contains the Bubble Tea update loop and helper workflow that
// react to incoming messages.
package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// Update processes Bubble Tea messages and owns the F1 dirty classification.
// Dirtiness is deny-by-default: every message type marks the frame dirty
// unless its handler provably changes only render-neutral bookkeeping.
//
// Render-neutral exemptions (fields verified unread by any render function):
//   - fastTickMsg: fastWatchActive/fastWatchGen, fastCaptureActive, preview
//     signal state; dispatching capture commands is not a visible change —
//     capture output arrives later as its own independently-dirty
//     paneContentMsg.
//   - tickMsg: inflight plus snapshot dispatch.
//   - runtimeTickMsg: runtimeInflight plus runtime-card dispatch.
//   - paneContentMsg and renderDeadlineMsg classify themselves by state delta
//     inside handleMessage (content actually changed / armed generation
//     matched) rather than by message type.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := m.handleMessage(msg)
	switch msg.(type) {
	case fastTickMsg, tickMsg, runtimeTickMsg, paneContentMsg, renderDeadlineMsg:
	default:
		m.markRenderDirty()
	}
	if m.renderDirty {
		if tick := m.scheduleRenderDeadline(); tick != nil {
			cmd = tea.Batch(cmd, tick)
		}
	}
	return model, cmd
}

// handleMessage routes messages to specialised handlers, returning the next
// command to execute. Handlers that change render-affecting state under a
// delta check (paneContentMsg, renderDeadlineMsg) call markRenderDirty
// themselves; everything else is dirtied by the Update wrapper.
func (m *Model) handleMessage(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.updatePreviewDimensions(m.filteredSessionCount())
	case tea.KeyMsg:
		if _, ok := msg.(tea.KeyPressMsg); !ok {
			return m, nil
		}
		if m.paletteOpen {
			return m.handlePaletteKey(msg)
		}
		if m.commanding {
			return m.handleCommandKey(msg)
		}
		if m.searching {
			return m.handleSearchKey(msg)
		}
		if handled, cmd := m.handleGlobalKey(msg); handled {
			return m, cmd
		}
		if handled, cmd := m.handleFocusedKey(msg); handled {
			return m, cmd
		}
	case tea.MouseMsg:
		return m.handleMouse(msg)
	case searchBlurMsg:
		m.searching = false
	case snapshotMsg:
		m.inflight = false
		m.err = nil
		m.lastUpdated = msg.snapshot.Timestamp
		m.refreshJanitorStatus()
		// Runtime cards merge from the async loader's cache; the snapshot
		// itself carries tmux sessions only (see fetchRuntimeCardsCmd).
		m.sessions = append(msg.snapshot.Sessions, m.runtimeSessions...)
		m.refreshArtifactOutcomes()
		m.refreshLifecycleVerdicts()
		m.paneParseWarnings = msg.snapshot.PaneParseWarnings
		m.pruneHiddenRuntimeSessions()
		if m.detailSession != "" && !m.sessionExists(m.detailSession) {
			m.leaveDetail(true)
		}
		for id := range m.collapsed {
			if !m.sessionExists(id) {
				delete(m.collapsed, id)
			}
		}
		m.updateStaleSessions()
		m.invalidateClassifications()
		cmd := m.ensurePreviewsAndCapture()
		m.updatePreviewDimensions(m.filteredSessionCount())
		return m, tea.Batch(scheduleTick(m.pollInterval), m.scheduleFastCaptureWatch(), cmd)
	case errMsg:
		m.inflight = false
		m.err = msg.err
		return m, scheduleTick(m.pollInterval)
	case statusMsg:
		m.showToast(string(msg))
	case paneContentMsg:
		if m.fastCaptureActive != nil {
			delete(m.fastCaptureActive, msg.sessionID)
		}
		if preview, ok := m.previews[msg.sessionID]; ok && preview.paneID == msg.paneID {
			content := strings.TrimRight(msg.text, "\n")
			if msg.err != nil {
				content = "Pane capture error: " + msg.err.Error()
			}
			if content != preview.lastContent {
				// Delta-classified dirty: only a real content change reaches
				// here; same-content deliveries stay render-neutral.
				m.markRenderDirty()
				wasAtBottom := preview.viewport.AtBottom()
				shouldFollow := preview.autoFollow || preview.lastContent == "" || wasAtBottom
				preview.viewport.SetContent(content)
				preview.lastContent = content
				preview.lastChanged = m.clockNow()
				if shouldFollow {
					preview.viewport.GotoBottom()
					preview.autoFollow = true
				}
				m.refreshPaneLifecycleVerdict(msg.sessionID, msg.paneID, content)
				if m.updateStaleSessions() {
					// The stale set is a cross-session classification input:
					// when it changes, every memoized entry may be affected.
					m.invalidateClassifications()
				} else {
					// Content and lifecycle verdict are per-session inputs;
					// other sessions' memoized classifications stay valid.
					m.invalidateClassification(msg.sessionID)
				}
			}
		}
	case paneVarsMsg:
		if preview, ok := m.previews[msg.sessionID]; ok && preview.paneID == msg.paneID {
			var before string
			if len(preview.vars) > 0 {
				before = formatPaneVariables(preview.vars)
			}
			if msg.err != nil {
				preview.vars = map[string]string{"error": msg.err.Error()}
			} else {
				preview.vars = msg.vars
			}
			var after string
			if len(preview.vars) > 0 {
				after = formatPaneVariables(preview.vars)
			}
			if after != before {
				m.markRenderDirty()
			}
		}
	case tickMsg:
		if m.inflight {
			return m, nil
		}
		m.inflight = true
		return m, fetchSnapshotCmd(m.client)
	case fastTickMsg:
		// The arriving message is the outstanding watcher returning; clear the
		// single-flight guard before dispatching so exactly one successor is
		// armed for this lineage.
		m.fastWatchActive = false
		cmd := m.ensureFastCaptures()
		return m, tea.Batch(m.scheduleFastCaptureWatch(), cmd)
	case renderDeadlineMsg:
		if msg.gen == m.renderDeadlineGen {
			// The armed time-transition tick fired: the frame's rendered
			// time bins / pulse / toast state just changed. The Update
			// wrapper re-arms for the next transition.
			m.armedRenderAt = time.Time{}
			m.markRenderDirty()
		}
		return m, nil
	case runtimeCardsMsg:
		m.runtimeInflight = false
		m.runtimeSessions = msg.sessions
		// The timeline diffs runtime cards against their previous load; it
		// must run here, not on snapshotMsg, now that snapshots do not carry
		// runtime cards.
		m.updateRuntimeTimeline(tmux.Snapshot{Timestamp: msg.loadedAt, Sessions: msg.sessions})
		return m, scheduleRuntimeTick(m.runtime.Interval)
	case runtimeTickMsg:
		if !m.runtime.Enabled || m.runtimeInflight {
			return m, nil
		}
		m.runtimeInflight = true
		return m, fetchRuntimeCardsCmd(m.runtime)
	}
	return m, nil
}

// scheduleFastCaptureWatch arms the off-loop pane-log watcher. It is
// single-flight: while a watcher command is outstanding no caller (snapshot
// tick or fast tick) can arm a second lineage, so watcher goroutines cannot
// accumulate over long-lived agent sessions.
func (m *Model) scheduleFastCaptureWatch() tea.Cmd {
	if m == nil || m.fastWatchActive || !m.hasFastCaptureCandidates() {
		return nil
	}
	signals, missingSignal, inflightSkipped := m.fastCaptureSignals()
	fallback := fastCaptureIdleTick
	deadline := fastCaptureIdleTick
	if inflightSkipped {
		// A capture is outstanding for at least one watched session. Keep the
		// watch cycle at sweep cadence so the session re-enters the signal set
		// within one frame of its capture completing; the dispatch-side signal
		// and fallback throttles still bound the actual capture rate.
		fallback = fastCaptureInterval
		deadline = fastCaptureInterval
	} else if len(signals) == 0 && missingSignal {
		fallback = fastCaptureFallback
	}
	m.fastWatchActive = true
	m.fastWatchGen++
	return scheduleFastCaptureWatch(signals, fallback, deadline)
}

func (m *Model) hasFastCaptureCandidates() bool {
	if m == nil {
		return false
	}
	for _, session := range m.sessions {
		if !m.isHidden(session.ID) && m.shouldFastCapture(session) {
			return true
		}
	}
	return false
}

func (m *Model) ensureFastCaptures() tea.Cmd {
	if m == nil || len(m.sessions) == 0 {
		return nil
	}
	if m.fastCaptureActive == nil {
		m.fastCaptureActive = make(map[string]struct{})
	}
	var cmds []tea.Cmd
	for _, session := range m.sessions {
		if m.isHidden(session.ID) || !m.shouldFastCapture(session) {
			continue
		}
		if _, ok := m.fastCaptureActive[session.ID]; ok {
			continue
		}
		collapsed := m.isCollapsed(session.ID)
		isFocused := session.ID == m.focusedSession
		inDetail := m.viewMode == viewModeDetail && m.detailSession == session.ID
		if collapsed && !isFocused && !inDetail {
			continue
		}
		window, ok := activeWindow(session)
		if !ok {
			continue
		}
		pane, ok := activePane(window)
		if !ok {
			continue
		}
		preview := m.previews[session.ID]
		if preview == nil || preview.paneID != pane.ID {
			continue
		}
		if !m.fastCaptureSignalChanged(session, pane, preview) {
			continue
		}
		m.fastCaptureActive[session.ID] = struct{}{}
		cmds = append(cmds, fetchPaneContentCmd(m.client, session.ID, pane.ID, captureLinesFor(preview.viewport.Height())))
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

// fastCaptureSignals builds the watcher's signal set. It must stay aligned
// with the dispatch set: any session ensureFastCaptures would skip (in-flight
// capture, collapsed card, missing preview) is excluded here, because a
// watched signal that dispatch never consumes would re-fire the watcher on
// every sweep. inflightSkipped reports the in-flight exclusions so the
// scheduler can shorten the watch deadline instead of stalling those sessions
// until the idle tick.
func (m *Model) fastCaptureSignals() ([]fastCaptureSignal, bool, bool) {
	if m == nil || len(m.sessions) == 0 {
		return nil, false, false
	}
	signals := make([]fastCaptureSignal, 0)
	missingSignal := false
	inflightSkipped := false
	for _, session := range m.sessions {
		if m.isHidden(session.ID) || !m.shouldFastCapture(session) {
			continue
		}
		if _, ok := m.fastCaptureActive[session.ID]; ok {
			inflightSkipped = true
			continue
		}
		collapsed := m.isCollapsed(session.ID)
		isFocused := session.ID == m.focusedSession
		inDetail := m.viewMode == viewModeDetail && m.detailSession == session.ID
		if collapsed && !isFocused && !inDetail {
			continue
		}
		window, ok := activeWindow(session)
		if !ok {
			continue
		}
		pane, ok := activePane(window)
		if !ok {
			continue
		}
		preview := m.previews[session.ID]
		if preview == nil || preview.paneID != pane.ID {
			continue
		}
		path := paneOutputSignalPath(session, pane)
		if path == "" {
			missingSignal = true
			continue
		}
		recorded := preview.signal.path == path
		signals = append(signals, fastCaptureSignal{
			sessionID: session.ID,
			path:      path,
			size:      preview.signal.size,
			modTime:   preview.signal.modTime,
			seen:      recorded && preview.signal.seen,
			statErr:   recorded && preview.signal.statErr,
		})
	}
	return signals, missingSignal, inflightSkipped
}

// ensurePreviewsAndCapture keeps track of per-session previews and captures
// fresh content for their active panes.
func (m *Model) ensurePreviewsAndCapture() tea.Cmd {
	captureOrder := m.captureOrder()
	active := make(map[string]struct{}, len(m.sessions))
	var cmds []tea.Cmd
	captureBudget := m.captureBudget
	if captureBudget <= 0 {
		captureBudget = maxCapturesPerTick
	}
	for _, session := range captureOrder {
		if m.isHidden(session.ID) {
			continue
		}
		active[session.ID] = struct{}{}

		collapsed := m.isCollapsed(session.ID)
		isFocused := session.ID == m.focusedSession
		inDetail := m.viewMode == viewModeDetail && m.detailSession == session.ID
		window, ok := activeWindow(session)
		if !ok {
			continue
		}
		pane, ok := activePane(window)
		if !ok {
			continue
		}
		preview := m.previews[session.ID]
		if preview == nil {
			// New previews start at card dimensions, not terminal dimensions:
			// the render pass owns per-card viewport sizing, and captures size
			// themselves from viewport height (captureLinesFor).
			vp := viewportFor(innerDimension{
				width:  m.cardInnerWidth,
				height: m.cardInnerHeight,
			})
			preview = &sessionPreview{viewport: &vp, lastChanged: m.clockNow(), autoFollow: true}
			m.previews[session.ID] = preview
		}
		if preview.paneID != pane.ID {
			preview.viewport.SetContent("")
			preview.paneID = pane.ID
			preview.lastContent = ""
			preview.vars = nil
			preview.autoFollow = true
		}
		if content := cardSafeBlock(strings.TrimRight(pane.PreviewText, "\n")); content != "" {
			if content != preview.lastContent {
				shouldFollow := preview.autoFollow || preview.lastContent == "" || preview.viewport.AtBottom()
				preview.viewport.SetContent(content)
				preview.lastContent = content
				preview.lastChanged = m.clockNow()
				if shouldFollow {
					preview.viewport.GotoBottom()
					preview.autoFollow = true
				}
			}
			continue
		}
		shouldCapture := true
		if collapsed && !isFocused && !inDetail {
			shouldCapture = false
		}

		prioritized := isFocused || inDetail || m.shouldFastCapture(session)
		if shouldCapture {
			if prioritized {
				// Focused/detail/live-agent panes are the wall's real-time path.
				// Leave the rotating budget for background runtime/service cards.
			} else if captureBudget <= 0 {
				shouldCapture = false
			} else {
				captureBudget--
			}
		}
		if shouldCapture {
			lines := captureLinesFor(preview.viewport.Height())
			cmds = append(cmds, fetchPaneContentCmd(m.client, session.ID, pane.ID, lines))
		}
		if session.ID == m.focusedSession {
			cmds = append(cmds, fetchPaneVarsCmd(m.client, session.ID, pane.ID))
		}
	}
	for sessionID := range m.previews {
		if _, ok := active[sessionID]; !ok {
			delete(m.previews, sessionID)
			delete(m.fastCaptureActive, sessionID)
		}
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}

func (m *Model) pruneHiddenRuntimeSessions() {
	for id := range m.hidden {
		if strings.HasPrefix(id, "openclaw-runtime:") && !m.sessionExists(id) {
			delete(m.hidden, id)
		}
	}
}

// captureOrder returns sessions in the order we should attempt pane captures,
// prioritising focused/detail/live-agent sessions and rotating through the rest
// so background work is spread across ticks.
func (m *Model) captureOrder() []tmux.Session {
	if len(m.sessions) == 0 {
		return nil
	}

	ordered := make([]tmux.Session, 0, len(m.sessions))
	seen := make(map[string]struct{}, len(m.sessions))

	add := func(id string) {
		if id == "" {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		if session, ok := m.sessionByID(id); ok {
			ordered = append(ordered, session)
			seen[id] = struct{}{}
		}
	}

	add(m.focusedSession)
	if m.viewMode == viewModeDetail {
		add(m.detailSession)
	}
	add(m.cursorSession)

	start := 0
	if len(m.sessions) > 0 {
		start = m.captureOffset % len(m.sessions)
	}
	for i := 0; i < len(m.sessions); i++ {
		session := m.sessions[(start+i)%len(m.sessions)]
		if _, ok := seen[session.ID]; ok {
			continue
		}
		if m.shouldFastCapture(session) {
			ordered = append(ordered, session)
			seen[session.ID] = struct{}{}
		}
	}
	for i := 0; i < len(m.sessions); i++ {
		session := m.sessions[(start+i)%len(m.sessions)]
		if _, ok := seen[session.ID]; ok {
			continue
		}
		ordered = append(ordered, session)
	}

	if len(m.sessions) > 0 {
		m.captureOffset = (start + 1) % len(m.sessions)
	}

	return ordered
}

func (m *Model) shouldFastCapture(session tmux.Session) bool {
	return cockpitGroupFor(m, session).name == groupActiveAgents.name
}

func (m *Model) fastCaptureSignalChanged(session tmux.Session, pane tmux.Pane, preview *sessionPreview) bool {
	if preview == nil {
		return true
	}
	path := paneOutputSignalPath(session, pane)
	if path == "" {
		return fallbackFastCaptureDue(preview)
	}
	info, err := os.Stat(path)
	if err != nil {
		// Record the failed stat as observed signal state so the watcher sees
		// a known-bad path (no change) instead of an unseen signal that fires
		// on every sweep; a later successful stat reads as a change.
		if preview.signal.path != path || !preview.signal.statErr {
			preview.signal = outputSignalState{
				path:         path,
				seen:         true,
				statErr:      true,
				lastFallback: preview.signal.lastFallback,
			}
		}
		return fallbackFastCaptureDue(preview)
	}
	modTime := info.ModTime()
	size := info.Size()
	if preview.signal.path != path {
		preview.signal = outputSignalState{path: path, size: size, modTime: modTime, seen: true}
		return true
	}
	if preview.signal.statErr || !preview.signal.seen || preview.signal.size != size || !preview.signal.modTime.Equal(modTime) {
		preview.signal = outputSignalState{path: path, size: size, modTime: modTime, seen: true}
		return true
	}
	return false
}

func fallbackFastCaptureDue(preview *sessionPreview) bool {
	if preview == nil {
		return true
	}
	now := time.Now()
	if preview.signal.lastFallback.IsZero() || now.Sub(preview.signal.lastFallback) >= 250*time.Millisecond {
		preview.signal.lastFallback = now
		return true
	}
	return false
}

func paneOutputSignalPath(session tmux.Session, pane tmux.Pane) string {
	if pane.Cockpit == nil {
		return ""
	}
	if path := strings.TrimSpace(pane.Cockpit.PaneLog); path != "" {
		return path
	}
	runRoot := strings.TrimSpace(pane.Cockpit.RunRoot)
	if runRoot == "" || strings.TrimSpace(session.Name) == "" {
		return ""
	}
	return filepath.Join(runRoot, "logs", session.Name+"-pane.log")
}

// filteredSessions applies the active search filter and hidden toggles to the
// current snapshot.
func (m *Model) filteredSessions() []tmux.Session {
	if m.viewMode == viewModeDetail && m.detailSession != "" {
		if session, ok := m.sessionByID(m.detailSession); ok {
			return []tmux.Session{session}
		}
		m.leaveDetail(true)
	}
	return m.filteredSessionsFull()
}

func (m *Model) filteredSessionsFull() []tmux.Session {
	var out []tmux.Session
	query := strings.ToLower(m.searchQuery)
	for _, session := range m.sessions {
		if m.isHidden(session.ID) {
			continue
		}
		if !m.sessionMatchesViewFilter(session) {
			continue
		}
		if query == "" || sessionMatches(session, query) {
			out = append(out, session)
		}
	}
	if m.organized {
		sortSessionsForCockpit(m, out)
	}
	return out
}

func (m *Model) sessionMatchesViewFilter(session tmux.Session) bool {
	switch m.viewFilter {
	case "":
		return true
	case "decision":
		return cockpitGroupFor(m, session).name == groupOperationalFailures.name || sessionAttentionState(m, session) == "awaiting-operator"
	case "route":
		return sessionRuntimePresentationGroup(session) == "route_health"
	case "handoff":
		return sessionRuntimePresentationGroup(session) == "delivery_handoff"
	case "services":
		return cockpitGroupFor(m, session).name == groupServices.name
	default:
		return true
	}
}

// filteredSessionCount provides a quick count for layout calculations.
func (m *Model) filteredSessionCount() int {
	return len(m.filteredSessions())
}

// captureLinesFor determines how many lines to capture for a viewport height.
func captureLinesFor(height int) int {
	lines := minCaptureLines
	if height > 0 {
		lines = height + captureSlackLines
	}
	if lines < minCaptureLines {
		lines = minCaptureLines
	}
	if lines > maxCaptureLines {
		lines = maxCaptureLines
	}
	return lines
}
