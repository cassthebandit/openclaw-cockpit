// File sessions.go offers helpers for navigating through snapshot structures.
package ui

import (
	"strings"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// sessionMatches reports whether a session, its windows, or panes contain the
// provided query string.
func sessionMatches(session tmux.Session, query string) bool {
	if strings.Contains(strings.ToLower(session.Name), query) {
		return true
	}
	for _, window := range session.Windows {
		if strings.Contains(strings.ToLower(window.Name), query) {
			return true
		}
		for _, pane := range window.Panes {
			if strings.Contains(strings.ToLower(pane.TitleOrCmd()), query) {
				return true
			}
			if cockpitMatches(pane.Cockpit, query) {
				return true
			}
		}
	}
	return false
}

func cockpitMatches(meta *tmux.CockpitMeta, query string) bool {
	if meta == nil {
		return false
	}
	fields := []string{
		meta.Kind,
		meta.Agent,
		meta.Owner,
		meta.Project,
		meta.Goal,
		meta.State,
		meta.DisplayStatus,
		meta.DisplayGroup,
		meta.PresentationGroup,
		meta.PresentationLabel,
		meta.Reason,
		meta.NextAction,
		meta.WhyVisible,
		meta.SuggestionKind,
		meta.SuggestedAction,
		meta.SuggestedCommand,
		meta.SuggestionConfidence,
		meta.Skeleton,
		meta.SkeletonReason,
		meta.Suppressed,
		meta.RunRoot,
		meta.ThreadID,
		meta.SessionID,
		meta.StartedAt,
		meta.UpdatedAt,
		meta.ExitCode,
		meta.WhyHeadless,
		meta.ProgressPath,
		meta.EndReason,
		meta.RouteFailure,
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), query) {
			return true
		}
	}
	return false
}

// activeWindow picks the active window for a session or falls back to the
// first window in the slice.
func activeWindow(session tmux.Session) (tmux.Window, bool) {
	for _, window := range session.Windows {
		if window.Active {
			return window, true
		}
	}
	if len(session.Windows) > 0 {
		return session.Windows[0], true
	}
	return tmux.Window{}, false
}

// activePane selects the active pane, defaulting to the first pane when none
// are marked active.
func activePane(window tmux.Window) (tmux.Pane, bool) {
	for _, pane := range window.Panes {
		if pane.Active {
			return pane, true
		}
	}
	if len(window.Panes) > 0 {
		return window.Panes[0], true
	}
	return tmux.Pane{}, false
}

// sessionAllPanesDead reports whether every pane in the session has exited.
func sessionAllPanesDead(session tmux.Session) bool {
	found := false
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			found = true
			if !pane.Dead {
				return false
			}
		}
	}
	return found
}

// sessionLatestActivity returns the most recent activity timestamp within a
// session.
func sessionLatestActivity(session tmux.Session) time.Time {
	var latest time.Time
	for _, window := range session.Windows {
		for _, pane := range window.Panes {
			ts := pane.LastActivity
			if ts.After(latest) {
				latest = ts
			}
		}
	}
	return latest
}
