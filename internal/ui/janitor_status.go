package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

const (
	janitorStatusStaleAfter = 3 * time.Minute
	// janitorStatusCapBytes bounds how much of the status sidecar is read
	// before JSON decoding. A healthy sidecar is a few KB; a runaway or
	// hostile file past the cap is reported as invalid instead of being
	// slurped into memory.
	janitorStatusCapBytes = 4 << 20 // 4 MiB
)

type janitorStatusFile struct {
	StatusVersion int                             `json:"status_version"`
	GeneratedAt   string                          `json:"generated_at"`
	IntervalS     int                             `json:"interval_s"`
	Policy        string                          `json:"policy"`
	Sessions      map[string]janitorSessionStatus `json:"sessions"`
	LastCycle     janitorCycleStatus              `json:"last_cycle"`
}

type janitorSessionStatus struct {
	JanitorState  string `json:"janitor_state"`
	MarkedAt      string `json:"marked_at"`
	Reason        string `json:"reason"`
	KillNotBefore string `json:"kill_not_before"`
	LastAction    string `json:"last_action"`
	LastRefusal   string `json:"last_refusal"`
	// PaneID and PaneCreated carry the sidecar's pane identity for the row.
	// Hygiene writes pane_id from tmux #{pane_id} and pane_created from the
	// primary pane's #{session_created}; a row may only grant teardown or
	// countdown authority when this identity matches a current pane
	// (janitorSessionRow), so a replacement pane reusing the session name never
	// inherits the old row's cleanup truth.
	PaneID      string `json:"pane_id"`
	PaneCreated string `json:"pane_created"`
}

type janitorCycleStatus struct {
	Policy     string `json:"policy"`
	Kill       int    `json:"kill"`
	Mark       int    `json:"mark"`
	CancelMark int    `json:"cancel_mark"`
	Skip       int    `json:"skip"`
	Refuse     int    `json:"refuse"`
}

type janitorStatusView struct {
	Path      string
	State     string
	Detail    string
	Generated time.Time
	Sessions  map[string]janitorSessionStatus
	Cycle     janitorCycleStatus
}

func loadJanitorStatusFile(path string, now time.Time, staleAfter time.Duration) janitorStatusView {
	if staleAfter <= 0 {
		staleAfter = janitorStatusStaleAfter
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return janitorStatusView{State: "disabled"}
	}
	view := janitorStatusView{Path: path, State: "missing"}
	file, err := os.Open(path)
	if err != nil {
		view.Detail = err.Error()
		return view
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, janitorStatusCapBytes+1))
	if err != nil {
		view.State = "invalid"
		view.Detail = err.Error()
		return view
	}
	if len(data) > janitorStatusCapBytes {
		view.State = "invalid"
		view.Detail = fmt.Sprintf("status file exceeds the %d byte cap", janitorStatusCapBytes)
		return view
	}
	var payload janitorStatusFile
	if err := json.Unmarshal(data, &payload); err != nil {
		view.State = "invalid"
		view.Detail = err.Error()
		return view
	}
	if payload.StatusVersion != 1 {
		view.State = "invalid"
		view.Detail = fmt.Sprintf("status_version %d", payload.StatusVersion)
		return view
	}
	generated := parseCockpitTimestamp(payload.GeneratedAt)
	if generated.IsZero() {
		view.State = "invalid"
		view.Detail = "generated_at missing"
		return view
	}
	view.Generated = generated
	view.Sessions = payload.Sessions
	view.Cycle = payload.LastCycle
	if now.Sub(generated) > staleAfter {
		view.State = "stale"
		view.Detail = coarseDuration(now.Sub(generated)) + " old"
		return view
	}
	view.State = "ok"
	return view
}

// janitorRenderKey captures exactly the fields janitorStatusLine renders, so
// refreshJanitorStatus can dirty the frame precisely when the file-backed
// derived value changes what is drawn.
type janitorRenderKey struct {
	state  string
	detail string
	cycle  janitorCycleStatus
}

func (v janitorStatusView) renderKey() janitorRenderKey {
	return janitorRenderKey{state: v.State, detail: v.Detail, cycle: v.Cycle}
}

func (m *Model) refreshJanitorStatus() {
	if m == nil || strings.TrimSpace(m.janitorStatusPath) == "" {
		return
	}
	previous := m.janitorStatus
	m.janitorStatus = loadJanitorStatusFile(m.janitorStatusPath, m.clockNow(), m.janitorStaleAfter)
	if m.janitorStatus.renderKey() != previous.renderKey() {
		// External file-backed render input changed its derived rendered
		// value: dirty the frame even if the triggering message would not.
		m.markRenderDirty()
	}
	// Group classification consumes sidecar session rows, so a new sidecar
	// generation or a freshness-state change can move cards between groups.
	if m.janitorStatus.State != previous.State || !m.janitorStatus.Generated.Equal(previous.Generated) {
		m.invalidateClassifications()
	}
}

func (m *Model) janitorStatusLine(width int) string {
	if m == nil || m.janitorStatus.State == "" || m.janitorStatus.State == "disabled" {
		return ""
	}
	status := m.janitorStatus
	parts := []string{"janitor: " + status.State}
	if status.Cycle.Mark > 0 {
		parts = append(parts, fmt.Sprintf("marked %d", status.Cycle.Mark))
	}
	if status.Cycle.Kill > 0 {
		parts = append(parts, fmt.Sprintf("cleanup pending %d", status.Cycle.Kill))
	}
	if status.Cycle.Refuse > 0 {
		parts = append(parts, fmt.Sprintf("held/refused %d", status.Cycle.Refuse))
	}
	if status.Detail != "" {
		parts = append(parts, status.Detail)
	}
	line := strings.Join(parts, " · ")
	if width > 0 {
		// Display-width-safe truncation: byte slicing can split a multibyte
		// rune and emit a broken tail into the footer.
		return truncateSingleLine(line, width)
	}
	// Detail can carry external error text; sanitize even without truncation.
	return cardSafeLine(line)
}
