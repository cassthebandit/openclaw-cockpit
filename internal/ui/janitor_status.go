package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const janitorStatusStaleAfter = 3 * time.Minute

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

func loadJanitorStatusFile(path string, now time.Time) janitorStatusView {
	path = strings.TrimSpace(path)
	if path == "" {
		return janitorStatusView{State: "disabled"}
	}
	view := janitorStatusView{Path: path, State: "missing"}
	data, err := os.ReadFile(path)
	if err != nil {
		view.Detail = err.Error()
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
	if now.Sub(generated) > janitorStatusStaleAfter {
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
	previous := m.janitorStatus.renderKey()
	m.janitorStatus = loadJanitorStatusFile(m.janitorStatusPath, m.clockNow())
	if m.janitorStatus.renderKey() != previous {
		// External file-backed render input changed its derived rendered
		// value: dirty the frame even if the triggering message would not.
		m.markRenderDirty()
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
	if width > 0 && len(line) > width {
		return line[:max(0, width-3)] + "..."
	}
	return line
}
