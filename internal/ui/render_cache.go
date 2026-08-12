// File render_cache.go owns the F1 frame dirty-flag and view cache: deny-by-
// default dirty accounting, the render-neutral heartbeat exemption, and
// scheduled invalidation for time-dependent rendered text (coarse duration
// bins, pulse expiry, toast expiry, cleanup countdowns).
package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// renderDeadlineMsg is the scheduled time-transition tick. Only the message
// carrying the currently armed generation dirties the frame; superseded
// generations are render-neutral no-ops.
type renderDeadlineMsg struct{ gen uint64 }

// clockNow returns the model's time source. Tests inject m.clock for
// deterministic time-bin, pulse, and cache-expiry behavior; nil means
// time.Now.
func (m *Model) clockNow() time.Time {
	if m != nil && m.clock != nil {
		return m.clock()
	}
	return time.Now()
}

// markRenderDirty flags that render-affecting model state changed, so the next
// View() must rebuild the frame instead of serving the cache. Classification
// and filter invalidation route through this too (invalidateClassification*),
// keeping F1 dirtiness and F6/F7 invalidation one accounting system.
func (m *Model) markRenderDirty() {
	if m == nil {
		return
	}
	m.renderDirty = true
}

// scheduleRenderDeadline recomputes the earliest scheduled time-based render
// transition for the current model state and arms a single-flight tea.Tick
// for it. It is called from dirty update paths only — never from View — so
// clean cached frames cannot spawn timers, and an already-armed tick that
// still covers the earliest transition is left alone.
func (m *Model) scheduleRenderDeadline() tea.Cmd {
	now := m.clockNow()
	next := m.computeNextRenderTransition(now)
	m.nextRenderAt = next
	if next.IsZero() {
		return nil
	}
	if !m.armedRenderAt.IsZero() && m.armedRenderAt.After(now) && !m.armedRenderAt.After(next) {
		return nil
	}
	m.renderDeadlineGen++
	gen := m.renderDeadlineGen
	m.armedRenderAt = next
	delay := next.Sub(now)
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		return renderDeadlineMsg{gen: gen}
	})
}

// computeNextRenderTransition returns the earliest future instant at which the
// rendered frame changes without any message arriving: toast expiry, the title
// bar refreshed-ago bin edge, per-pane last/done/launched coarse-duration bin
// edges, cleanup countdown transitions, and pulse expiries. A zero time means
// no scheduled transition. All sessions are scanned, including hidden or
// collapsed ones: a spuriously early rebuild is safe, a missed transition is a
// stale frame.
func (m *Model) computeNextRenderTransition(now time.Time) time.Time {
	var next time.Time
	earlier := func(t time.Time) {
		if t.IsZero() || !t.After(now) {
			return
		}
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}

	if m.toast != nil && m.toast.text != "" && m.toast.exp.After(now) {
		earlier(m.toast.exp)
	}
	if !m.lastUpdated.IsZero() {
		earlier(now.Add(nextCoarseDurationChange(now.Sub(m.lastUpdated))))
	}
	for _, session := range m.sessions {
		for _, window := range session.Windows {
			for _, pane := range window.Panes {
				if !pane.LastActivity.IsZero() {
					earlier(now.Add(nextCoarseDurationChange(now.Sub(pane.LastActivity))))
				}
				if pane.Cockpit == nil {
					continue
				}
				if doneAt := parseCockpitTimestamp(pane.Cockpit.CompletedAt); !doneAt.IsZero() {
					earlier(now.Add(nextCoarseDurationChange(now.Sub(doneAt))))
				} else if startedAt := parseCockpitTimestamp(pane.Cockpit.StartedAt); !startedAt.IsZero() {
					earlier(now.Add(nextCoarseDurationChange(now.Sub(startedAt))))
				}
				if markedAt := parseCockpitTimestamp(pane.Cockpit.TeardownMarkedAt); !markedAt.IsZero() {
					// Countdown transitions come from the janitor sidecar's
					// kill_not_before — the same source the card renders.
					if row, join := m.janitorSessionRow(session); join == janitorJoinOK {
						if killAt := parseCockpitTimestamp(row.KillNotBefore); !killAt.IsZero() {
							if remaining := killAt.Sub(now); remaining > 0 {
								if delta, ok := nextCountdownChange(remaining); ok {
									earlier(now.Add(delta))
								}
							}
						}
					}
				}
			}
		}
	}
	for _, preview := range m.previews {
		if preview == nil || preview.lastChanged.IsZero() {
			continue
		}
		if expiry := preview.lastChanged.Add(pulseDuration); expiry.After(now) {
			earlier(expiry)
		}
	}
	return next
}

// nextCoarseDurationChange returns how long until coarseDuration(elapsed)
// next changes for an increasing elapsed duration (a "since" label). The
// result is always positive.
func nextCoarseDurationChange(elapsed time.Duration) time.Duration {
	var next time.Duration
	switch {
	case elapsed < 5*time.Second:
		next = 5 * time.Second
	case elapsed < time.Minute:
		next = (elapsed/(5*time.Second) + 1) * 5 * time.Second
	case elapsed < time.Hour:
		next = (elapsed/time.Minute + 1) * time.Minute
	default:
		next = (elapsed/time.Hour + 1) * time.Hour
	}
	delta := next - elapsed
	if delta <= 0 {
		delta = time.Millisecond
	}
	return delta
}

// nextCountdownChange returns how long until coarseDuration(remaining) next
// changes for a decreasing remaining duration (a countdown label), including
// the final flip to "cleanup pending" as remaining reaches zero. ok is false
// when the countdown has already finished and the label is static.
func nextCountdownChange(remaining time.Duration) (time.Duration, bool) {
	if remaining <= 0 {
		return 0, false
	}
	var floor time.Duration
	switch {
	case remaining < 5*time.Second:
		floor = 0
	case remaining < time.Minute:
		floor = remaining / (5 * time.Second) * (5 * time.Second)
	case remaining < time.Hour:
		floor = remaining / time.Minute * time.Minute
	default:
		floor = remaining / time.Hour * time.Hour
	}
	delta := remaining - floor
	if delta <= 0 {
		delta = time.Millisecond
	}
	return delta, true
}
