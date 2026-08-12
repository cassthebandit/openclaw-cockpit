package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// buildBudgetWall creates n continuously-changing managed agents (Active
// Agents group) whose pane-log signal files live under dir, with previews
// wired so the fast-capture path is fully eligible.
func buildBudgetWall(t *testing.T, m *Model, dir string, n int) []string {
	t.Helper()
	paths := make([]string, 0, n)
	for i := 0; i < n; i++ {
		name := fmt.Sprintf("agent-%02d", i)
		session := agentSessionForGroup(name, "running")
		path := filepath.Join(dir, name+".log")
		if err := os.WriteFile(path, []byte("seed"), 0o644); err != nil {
			t.Fatalf("seed log: %v", err)
		}
		session.Windows[0].Panes[0].Cockpit.PaneLog = path
		m.sessions = append(m.sessions, session)
		vp := viewportFor(innerDimension{width: 40, height: 8})
		m.previews[session.ID] = &sessionPreview{
			viewport:   &vp,
			paneID:     session.Windows[0].Panes[0].ID,
			autoFollow: true,
		}
		paths = append(paths, path)
	}
	return paths
}

func appendAll(t *testing.T, paths []string) {
	t.Helper()
	for _, path := range paths {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("open log: %v", err)
		}
		if _, err := f.WriteString("x"); err != nil {
			t.Fatalf("append log: %v", err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close log: %v", err)
		}
	}
}

// TestAggregateFastCaptureBudgetIsFairAcrossAgents is the AC6 deterministic
// one-second simulation: N continuously changing managed-agent logs, 60
// fast sweeps on an injected clock (default-FPS cadence), captures completing
// before the next sweep. It asserts the aggregate cap, full coverage, at most
// one service of skew between continuously dirty peers, and at least two
// services each for groups of 20 or fewer. This drives the fast path only —
// no snapshot tick — so the counts are fast-path dispatches.
func TestAggregateFastCaptureBudgetIsFairAcrossAgents(t *testing.T) {
	for _, n := range []int{10, 20, 25, 30} {
		n := n
		t.Run(fmt.Sprintf("agents-%d", n), func(t *testing.T) {
			t.Parallel()
			simNow := time.Unix(1752000000, 0)
			m := NewModel(nil, time.Second, 4, nil, false, true)
			m.SetOrganized(true)
			m.clock = func() time.Time { return simNow }
			paths := buildBudgetWall(t, m, t.TempDir(), n)

			counts := make(map[string]int, n)
			total := 0
			for step := 0; step < 60; step++ {
				appendAll(t, paths) // every agent keeps changing continuously
				for _, request := range m.planFastCaptures() {
					counts[request.sessionID]++
					total++
					// The capture completes before the next sweep
					// (paneContentMsg clears the single-flight guard).
					delete(m.fastCaptureActive, request.sessionID)
				}
				simNow = simNow.Add(fastCaptureInterval)
			}

			if total > aggregateCaptureBudgetPerSecond {
				t.Fatalf("aggregate fast captures = %d, want <= %d", total, aggregateCaptureBudgetPerSecond)
			}
			minServed, maxServed := int(^uint(0)>>1), 0
			for _, session := range m.sessions {
				served := counts[session.ID]
				if served == 0 {
					t.Fatalf("agent %s was never serviced in the one-second window", session.ID)
				}
				if served < minServed {
					minServed = served
				}
				if served > maxServed {
					maxServed = served
				}
			}
			if maxServed-minServed > 1 {
				t.Fatalf("service skew %d-%d > 1 among continuously dirty agents: %v", maxServed, minServed, counts)
			}
			if n <= 20 && minServed < 2 {
				t.Fatalf("with %d agents every agent must be serviced at least twice, min = %d", n, minServed)
			}
		})
	}
}

// TestFastCaptureBudgetRefillsPerWindow pins the fixed one-second window
// behavior of the shared token bucket.
func TestFastCaptureBudgetRefillsPerWindow(t *testing.T) {
	t.Parallel()

	now := time.Unix(1752000000, 0)
	m := NewModel(nil, time.Second, 4, nil, false, true)
	for i := 0; i < aggregateCaptureBudgetPerSecond; i++ {
		if !m.takeCaptureToken(now) {
			t.Fatalf("token %d refused inside a fresh window", i)
		}
	}
	if m.takeCaptureToken(now.Add(900 * time.Millisecond)) {
		t.Fatal("budget must stay exhausted inside the same window")
	}
	if !m.takeCaptureToken(now.Add(time.Second)) {
		t.Fatal("budget must refill when the window rolls over")
	}
}

// TestSnapshotPathDispatchesNothingWhenBudgetExhausted proves the snapshot
// capture path draws from the same shared budget: with tokens exhausted it
// dispatches zero captures, while an identical wall with a fresh budget does
// dispatch.
func TestSnapshotPathDispatchesNothingWhenBudgetExhausted(t *testing.T) {
	t.Parallel()

	now := time.Unix(1752000000, 0)
	build := func() *Model {
		m := NewModel(nil, time.Second, 4, nil, false, true)
		m.clock = func() time.Time { return now }
		for i := 0; i < 3; i++ {
			session := sessionForGroup(fmt.Sprintf("plain-%d", i), "zsh", "/tmp", "")
			m.sessions = append(m.sessions, session)
			vp := viewportFor(innerDimension{width: 40, height: 8})
			m.previews[session.ID] = &sessionPreview{
				viewport:   &vp,
				paneID:     session.Windows[0].Panes[0].ID,
				autoFollow: true,
			}
		}
		return m
	}

	fresh := build()
	if cmd := fresh.ensurePreviewsAndCapture(); cmd == nil {
		t.Fatal("control: fresh budget should dispatch snapshot captures")
	}

	exhausted := build()
	for i := 0; i < aggregateCaptureBudgetPerSecond; i++ {
		exhausted.takeCaptureToken(now)
	}
	if cmd := exhausted.ensurePreviewsAndCapture(); cmd != nil {
		t.Fatal("snapshot path must dispatch zero captures when the shared budget is exhausted")
	}
}

// TestFastPathSharesBudgetWithSnapshotPath proves the two paths draw from one
// pool: snapshot-path consumption reduces what the fast path may dispatch in
// the same window.
func TestFastPathSharesBudgetWithSnapshotPath(t *testing.T) {
	t.Parallel()

	simNow := time.Unix(1752000000, 0)
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.clock = func() time.Time { return simNow }
	paths := buildBudgetWall(t, m, t.TempDir(), 5)

	// Snapshot path spends all but two tokens.
	for i := 0; i < aggregateCaptureBudgetPerSecond-2; i++ {
		m.takeCaptureToken(simNow)
	}
	appendAll(t, paths)
	requests := m.planFastCaptures()
	if len(requests) != 2 {
		t.Fatalf("fast path dispatched %d captures, want the 2 remaining shared tokens", len(requests))
	}
}
