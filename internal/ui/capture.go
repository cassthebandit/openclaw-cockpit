package ui

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

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

// takeCaptureToken consumes one token from the shared aggregate capture
// budget. Tokens refill in fixed one-second windows on the injectable clock,
// so at most aggregateCaptureBudgetPerSecond capture-pane subprocesses start
// per second across the fast and snapshot paths combined.
func (m *Model) takeCaptureToken(now time.Time) bool {
	if m == nil {
		return false
	}
	if m.captureWindowStart.IsZero() || now.Sub(m.captureWindowStart) >= time.Second {
		m.captureWindowStart = now
		m.captureTokensSpent = 0
	}
	if m.captureTokensSpent >= m.effectiveCaptureRate() {
		return false
	}
	m.captureTokensSpent++
	return true
}

// captureRequest is one planned capture-pane dispatch. planFastCaptures
// returns these so the deterministic budget/fairness tests can observe
// exactly which sessions were serviced without running subprocesses.
type captureRequest struct {
	generation uint64
	sessionID  string
	paneID     string
	lines      int
}

// planFastCaptures selects the fast-path capture dispatches for this sweep.
// It walks sessions round-robin from fastCaptureOffset and draws one shared
// budget token per dispatch; when the budget runs out mid-sweep the cursor
// parks on the first unserviced session, so continuously dirty sessions are
// serviced in strict rotation and service counts can differ by at most one.
func (m *Model) planFastCaptures() []captureRequest {
	if m == nil || len(m.sessions) == 0 {
		return nil
	}
	if m.fastCaptureActive == nil {
		m.fastCaptureActive = make(map[string]struct{})
	}
	now := m.clockNow()
	n := len(m.sessions)
	start := m.fastCaptureOffset % n
	var requests []captureRequest
	exhausted := false
	for i := 0; i < n; i++ {
		session := m.sessions[(start+i)%n]
		pane, preview, eligible, _ := m.fastCaptureTarget(session)
		if !eligible {
			continue
		}
		previousSignal := preview.signal
		if !m.fastCaptureSignalChanged(session, pane, preview) {
			continue
		}
		if !m.takeCaptureToken(now) {
			preview.signal = previousSignal // Observe only admitted captures.
			// Budget exhausted: park the cursor on this session so the next
			// window resumes exactly where rotation stopped.
			m.fastCaptureOffset = (start + i) % n
			exhausted = true
			break
		}
		requests = append(requests, m.admitCapture(session.ID, pane.ID, preview))
	}
	if !exhausted {
		m.fastCaptureOffset = (start + 1) % n
	}
	return requests
}

func (m *Model) ensureFastCaptures() tea.Cmd {
	requests := m.planFastCaptures()
	if len(requests) == 0 {
		return nil
	}
	cmds := make([]tea.Cmd, 0, len(requests))
	for _, request := range requests {
		cmds = append(cmds, fetchPaneContentCmd(m.client, request.sessionID, request.paneID, request.lines, request.generation))
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
		pane, preview, eligible, inflight := m.fastCaptureTarget(session)
		if inflight {
			inflightSkipped = true
		}
		if !eligible {
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
			delete(m.fastCaptureActive, session.ID)
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
		_, inFlight := m.fastCaptureActive[session.ID]
		shouldCapture := !inFlight
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
		// Snapshot-path captures draw from the same aggregate per-second
		// budget as the fast path; when it is exhausted nothing dispatches.
		if shouldCapture && !m.takeCaptureToken(m.clockNow()) {
			shouldCapture = false
		}
		if shouldCapture {
			request := m.admitCapture(session.ID, pane.ID, preview)
			cmds = append(cmds, fetchPaneContentCmd(m.client, request.sessionID, request.paneID, request.lines, request.generation))
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
		return m.fallbackFastCaptureDue(preview)
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
		return m.fallbackFastCaptureDue(preview)
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

func (m *Model) fallbackFastCaptureDue(preview *sessionPreview) bool {
	if preview == nil {
		return true
	}
	now := m.clockNow()
	if preview.signal.lastFallback.IsZero() || now.Sub(preview.signal.lastFallback) >= fastCaptureFallback {
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

// admitCapture is the single request owner for snapshot and fast captures.
// Generations survive pane replacement/removal, so late replies cannot clear
// another request's ownership or overwrite a newly created preview.
func (m *Model) admitCapture(sessionID, paneID string, preview *sessionPreview) captureRequest {
	if m.fastCaptureActive == nil {
		m.fastCaptureActive = make(map[string]struct{})
	}
	m.captureGeneration++
	preview.captureGeneration = m.captureGeneration
	m.fastCaptureActive[sessionID] = struct{}{}
	return captureRequest{sessionID: sessionID, paneID: paneID, lines: m.captureLines(preview.viewport.Height()), generation: m.captureGeneration}
}

// fastCaptureTarget is shared by admission and the signal watcher. Bookkeeping
// for budgets, fairness and missing signals deliberately remains at callers.
func (m *Model) fastCaptureTarget(session tmux.Session) (tmux.Pane, *sessionPreview, bool, bool) {
	if m.isHidden(session.ID) || !m.shouldFastCapture(session) {
		return tmux.Pane{}, nil, false, false
	}
	if _, ok := m.fastCaptureActive[session.ID]; ok {
		return tmux.Pane{}, nil, false, true
	}
	if m.isCollapsed(session.ID) && session.ID != m.focusedSession && (m.viewMode != viewModeDetail || m.detailSession != session.ID) {
		return tmux.Pane{}, nil, false, false
	}
	window, ok := activeWindow(session)
	if !ok {
		return tmux.Pane{}, nil, false, false
	}
	pane, ok := activePane(window)
	if !ok {
		return tmux.Pane{}, nil, false, false
	}
	preview := m.previews[session.ID]
	if preview == nil || preview.paneID != pane.ID {
		return tmux.Pane{}, nil, false, false
	}
	return pane, preview, true, false
}

func (m *Model) effectiveCaptureRate() int {
	if m.captureRate > 0 {
		return m.captureRate
	}
	return aggregateCaptureBudgetPerSecond
}

func (m *Model) captureLines(height int) int {
	if m.captureMinLines <= 0 || m.captureMaxLines <= 0 {
		return captureLinesFor(height)
	}
	if height <= 0 {
		return m.captureMinLines
	}
	return min(m.captureMaxLines, max(m.captureMinLines, height+m.captureSlackLines))
}
