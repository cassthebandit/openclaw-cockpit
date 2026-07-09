package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"
)

// TestCaptureGoldens renders a matrix of representative wall states and writes
// each frame plus its zone hit-box snapshot to COCKPIT_GOLDEN_DIR. It is the
// capture harness for the Run 1 byte-identity proof: run once against the
// packet-applied baseline and once after each implementation phase, then
// byte-compare the two directories. Skipped unless COCKPIT_GOLDEN_DIR is set.
//
// Determinism: all time anchors are expressed relative to a single capture-time
// now, so coarse-duration labels ("last 30s", "done 10m", "refreshed just
// now"), pulse state, and stale state come out identical across runs even
// though absolute wall-clock differs. Anchors sit mid-bin, never on a bin edge.
func TestCaptureGoldens(t *testing.T) {
	dir := os.Getenv("COCKPIT_GOLDEN_DIR")
	if dir == "" {
		t.Skip("COCKPIT_GOLDEN_DIR not set; golden capture skipped")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir golden dir: %v", err)
	}

	now := time.Now()

	type scenario struct {
		name  string
		build func(t *testing.T) *Model
	}

	scenarios := []scenario{
		{name: "organized-wall", build: func(t *testing.T) *Model {
			return goldenWallModel(t, now)
		}},
		{name: "hover", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.hoveredSession = "$agent-live-00"
			m.hoveredControl = fmt.Sprintf("%smax:%s", m.zonePrefix, "$agent-live-00")
			return m
		}},
		{name: "cursor-focus", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.cursorSession = "$agent-idle-01"
			m.focusedSession = "$agent-live-00"
			return m
		}},
		{name: "collapsed-card", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.collapsed["$agent-live-00"] = struct{}{}
			return m
		}},
		{name: "collapsed-groups", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			_ = m.View() // seed group collapse defaults first
			for _, g := range orderedCockpitGroups(m, m.filteredSessions()) {
				if g.name != groupActiveAgents.name {
					m.collapsedGroups[g.name] = struct{}{}
				}
			}
			// Direct state mutation after a render bypasses Update's dirty
			// classification; mark dirty so the capture View rebuilds. This
			// does not change rendered bytes for a given state.
			m.markRenderDirty()
			return m
		}},
		{name: "detail", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.enterDetail("$agent-live-00")
			return m
		}},
		{name: "search-filter", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.searchQuery = "agent-live"
			return m
		}},
		{name: "view-filter-decision", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.viewFilter = "decision"
			return m
		}},
		{name: "toast", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.showToast("golden toast message")
			return m
		}},
		{name: "palette", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.openCommandPalette()
			return m
		}},
		{name: "pulse", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			// Freshly changed content: pulse window (1500ms) is active at render.
			m.previews["$agent-live-00"].lastChanged = now
			return m
		}},
		{name: "cjk-emoji", build: func(t *testing.T) *Model {
			m := goldenWallModel(t, now)
			m.previews["$agent-live-00"].viewport.SetContent(cardSafeBlock(
				"\x1b[38;5;114m宽字符测试\x1b[0m 🚀 emoji row ✅\n" +
					"日本語のテキスト mixed with ascii\n" +
					"\x1b[38;5;179mmalformed escape \x1b[9 dangling\x1b[0m\n" +
					"tail line"))
			return m
		}},
	}

	for _, sc := range scenarios {
		sc := sc
		t.Run(sc.name, func(t *testing.T) {
			m := sc.build(t)
			view := m.View()
			framePath := filepath.Join(dir, sc.name+".frame.txt")
			if err := os.WriteFile(framePath, []byte(view.Content), 0o644); err != nil {
				t.Fatalf("write frame golden: %v", err)
			}
			zonesPath := filepath.Join(dir, sc.name+".zones.txt")
			if err := os.WriteFile(zonesPath, []byte(dumpZones()), 0o644); err != nil {
				t.Fatalf("write zone golden: %v", err)
			}
		})
	}
}

// dumpZones serializes the current global zone hit-box set sorted by id.
func dumpZones() string {
	snap := zone.DefaultManager.ZoneSnapshot()
	ids := make([]string, 0, len(snap))
	for id := range snap {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var b strings.Builder
	for _, id := range ids {
		info := snap[id]
		fmt.Fprintf(&b, "%s %d,%d..%d,%d\n", id, info.StartX, info.StartY, info.EndX, info.EndY)
	}
	return b.String()
}

// goldenWallModel builds a deterministic organized wall: live/idle/failed
// agents, runtime cards, services, shells, plus time-anchored metadata (last /
// done / launched / cleanup countdown), a stale session, and a pulse-expired
// preview set. All anchors are relative to now and sit mid-bin.
func goldenWallModel(tb testing.TB, now time.Time) *Model {
	tb.Helper()
	m := benchWallModel(tb, 12, 4)
	m.lastUpdated = now.Add(-2 * time.Second) // "refreshed just now"

	for i := range m.sessions {
		s := &m.sessions[i]
		switch {
		case strings.HasPrefix(s.Name, "agent-live-"):
			*s = withActivity(*s, now.Add(-32*time.Second)) // "last 30s"
		case strings.HasPrefix(s.Name, "agent-idle-"):
			*s = withActivity(*s, now.Add(-7*time.Minute)) // "last 7m"
			s.Windows[0].Panes[0].Cockpit.StartedAt = now.Add(-42*time.Minute).UTC().Format(time.RFC3339)
		case strings.HasPrefix(s.Name, "agent-fail-"):
			*s = withActivity(*s, now.Add(-2*time.Hour+-90*time.Second)) // "last 2h"
			s.Windows[0].Panes[0].Cockpit.CompletedAt = now.Add(-11*time.Minute).UTC().Format(time.RFC3339)
		}
	}

	// Cleanup countdown card: marked 2m ago, kill at +5m => "cleanup in 2m".
	marked := agentSessionForGroup("agent-marked-00", "running")
	marked.Windows[0].Panes[0].Cockpit.JanitorState = "marked_for_teardown"
	marked.Windows[0].Panes[0].Cockpit.TeardownMarkedAt = now.Add(-2*time.Minute - 20*time.Second).UTC().Format(time.RFC3339)
	marked.Windows[0].Panes[0].PreviewText = "teardown pending\n"
	m.sessions = append(m.sessions, marked)

	// Stale non-agent session: detached with old activity crosses staleThreshold.
	staleSession := sessionForGroup("old-workbench", "vim", "/Users/cass/notes", "notes")
	staleSession = withActivity(staleSession, now.Add(-3*time.Hour))
	m.sessions = append(m.sessions, staleSession)

	for _, s := range m.sessions[len(m.sessions)-2:] {
		vp := viewportFor(innerDimension{width: 48, height: 8})
		vp.SetContent(cardSafeBlock(strings.TrimRight(s.Windows[0].Panes[0].PreviewText, "\n")))
		vp.GotoBottom()
		m.previews[s.ID] = &sessionPreview{
			viewport:   &vp,
			paneID:     s.Windows[0].Panes[0].ID,
			autoFollow: true,
		}
	}

	// Pin every preview's lastChanged well outside the pulse window so cards do
	// not pulse unless a scenario opts in.
	for _, preview := range m.previews {
		preview.lastChanged = now.Add(-10 * time.Minute)
	}

	m.refreshArtifactOutcomes()
	m.refreshLifecycleVerdicts()
	m.updateStaleSessions()
	return m
}

// TestGoldenWallDeterminism proves the capture fixture is stable: two renders
// of independently built fixtures with the same relative anchors are
// byte-identical, so cross-run golden diffs can only come from code changes.
func TestGoldenWallDeterminism(t *testing.T) {
	now := time.Now()
	m1 := goldenWallModel(t, now)
	v1 := m1.View().Content
	m2 := goldenWallModel(t, now)
	v2 := m2.View().Content
	if v1 != v2 {
		t.Fatal("golden wall fixture is not deterministic across builds")
	}
}
