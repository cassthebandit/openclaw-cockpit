package ui

import (
	"slices"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func sessionBirthTime(session tmux.Session) time.Time {
	if !sessionHasOpenClawRuntime(session) {
		if window, ok := activeWindow(session); ok {
			if pane, ok := activePane(window); ok && pane.Cockpit != nil {
				if started, err := time.Parse(time.RFC3339, pane.Cockpit.StartedAt); err == nil {
					return started
				}
			}
		}
	}
	return session.CreatedAt
}

func (m *Model) rememberSessionBirths(sessions []tmux.Session) {
	if m.sessionBirth == nil {
		m.sessionBirth = make(map[string]time.Time)
	}
	observed := m.clockNow()
	for _, session := range sessions {
		if _, exists := m.sessionBirth[session.ID]; exists {
			continue
		}
		birth := sessionBirthTime(session)
		if birth.IsZero() {
			birth = observed
		}
		m.sessionBirth[session.ID] = birth
	}
}

// Logical order is oldest first. Paint each section in reverse so its oldest
// card occupies the bottom-right slot and additions fill left, then upward.
func (m *Model) stackedSessions(sessions []tmux.Session) []tmux.Session {
	if !m.organized || m.viewMode != viewModeOverview {
		return sessions
	}
	out := slices.Clone(sessions)
	for start := 0; start < len(out); {
		end := start + 1
		group := cockpitGroupFor(m, out[start])
		for end < len(out) && cockpitGroupFor(m, out[end]).name == group.name {
			end++
		}
		slices.Reverse(out[start:end])
		start = end
	}
	return out
}

func leadingStackSlots(count, columns int) int {
	return (columns - count%columns) % columns
}
