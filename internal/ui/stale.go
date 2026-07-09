// File stale.go encapsulates logic for detecting inactive or dead sessions.
package ui

import (
	"sort"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// updateStaleSessions recalculates which sessions qualify as stale and
// reports whether the stale set changed. The stale set is a cross-session
// classification input, so callers use the report to pick between global and
// per-session classification invalidation.
func (m *Model) updateStaleSessions() bool {
	previous := m.stale
	next := make(map[string]struct{}, len(previous))
	now := m.clockNow()
	for _, session := range m.sessions {
		if sessionHasOpenClawRuntime(session) {
			continue
		}
		if session.Attached {
			continue
		}
		if sessionAllPanesDead(session) {
			next[session.ID] = struct{}{}
			continue
		}
		last := m.sessionActivity(session)
		if last.IsZero() {
			continue
		}
		if now.Sub(last) >= staleThreshold {
			next[session.ID] = struct{}{}
		}
	}
	m.stale = next
	if len(next) != len(previous) {
		return true
	}
	for id := range next {
		if _, ok := previous[id]; !ok {
			return true
		}
	}
	return false
}

// isStale reports whether the provided session identifier is marked stale.
func (m *Model) isStale(sessionID string) bool {
	_, ok := m.stale[sessionID]
	return ok
}

// staleSessionNames returns sorted human-readable names for stale sessions.
func (m *Model) staleSessionNames() []string {
	if len(m.stale) == 0 {
		return nil
	}
	names := make([]string, 0, len(m.stale))
	for _, session := range m.sessions {
		if sessionHasOpenClawRuntime(session) {
			continue
		}
		if isQuietLiveServiceSession(session) {
			continue
		}
		if m.isStale(session.ID) {
			names = append(names, session.Name)
		}
	}
	sort.Strings(names)
	return names
}

// staleSessionIDs returns sorted identifiers for stale sessions.
func (m *Model) staleSessionIDs() []string {
	if len(m.stale) == 0 {
		return nil
	}
	ids := make([]string, 0, len(m.stale))
	for id := range m.stale {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (m *Model) sessionActivity(session tmux.Session) time.Time {
	latest := session.LastActivity
	if paneLatest := sessionLatestActivity(session); paneLatest.After(latest) {
		latest = paneLatest
	}
	if preview, ok := m.previews[session.ID]; ok && preview != nil {
		if preview.lastChanged.After(latest) {
			latest = preview.lastChanged
		}
	}
	return latest
}
