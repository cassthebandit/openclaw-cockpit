// File update.go contains the Bubble Tea update loop and helper workflow that
// react to incoming messages.
package ui

import (
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
		m.tmuxSessions = msg.snapshot.Sessions
		m.paneParseWarnings = msg.snapshot.PaneParseWarnings
		cmd := m.refreshMergedSessions()
		return m, tea.Batch(scheduleTick(m.pollInterval), m.scheduleFastCaptureWatch(), cmd)
	case errMsg:
		m.inflight = false
		m.err = msg.err
		return m, scheduleTick(m.pollInterval)
	case statusMsg:
		m.showToast(string(msg))
	case paneContentMsg:
		if preview, ok := m.previews[msg.sessionID]; ok && preview.paneID == msg.paneID && preview.captureGeneration == msg.generation {
			delete(m.fastCaptureActive, msg.sessionID)
			content := strings.TrimRight(msg.text, "\n")
			if msg.err != nil {
				// The error string is not pane output; strict-sanitize it
				// before it enters the preview as displayable text.
				content = "Pane capture error: " + cardSafeLine(msg.err.Error())
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
		cmd := m.refreshMergedSessions()
		return m, tea.Batch(scheduleRuntimeTick(m.runtime.Interval), cmd)
	case runtimeTickMsg:
		if !m.runtime.Enabled || m.runtimeInflight {
			return m, nil
		}
		m.runtimeInflight = true
		return m, fetchRuntimeCardsCmd(m.runtime)
	}
	return m, nil
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

// refreshMergedSessions applies either source independently without rescheduling polls.
func (m *Model) refreshMergedSessions() tea.Cmd {
	m.sessions = append(append([]tmux.Session(nil), m.tmuxSessions...), m.runtimeSessions...)
	m.rememberSessionBirths(m.sessions)
	for id := range m.sessionBirth {
		if !m.sessionExists(id) {
			delete(m.sessionBirth, id)
		}
	}
	m.refreshArtifactOutcomes()
	m.refreshLifecycleVerdicts()
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
	return cmd
}
