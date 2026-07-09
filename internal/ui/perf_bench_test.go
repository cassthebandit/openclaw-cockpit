package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	zone "github.com/cassthebandit/openclaw-cockpit/internal/zone"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// benchPaneBody builds ANSI-heavy pane content resembling agent CLI output.
// tail controls the closing lines so lifecycle classification exercises its
// real marker paths (active veto vs delivered-idle vs error).
func benchPaneBody(lines int, tail string) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		switch i % 4 {
		case 0:
			fmt.Fprintf(&b, "\x1b[38;5;114mOK\x1b[0m ran \x1b[1mgo test ./internal/pkg%02d\x1b[0m in 1.%02ds\n", i%17, i%60)
		case 1:
			fmt.Fprintf(&b, "\x1b[38;5;245m  reading file internal/ui/file%02d.go (%d lines)\x1b[0m\n", i%23, 100+i)
		case 2:
			fmt.Fprintf(&b, "\x1b[48;5;236m\x1b[38;5;252m tool \x1b[0m \x1b[38;5;179mEdit\x1b[0m applied hunk %d/%d cleanly\n", i%5, 5)
		default:
			fmt.Fprintf(&b, "plain progress line %d with no styling at all, width filler text here\n", i)
		}
	}
	b.WriteString(tail)
	return b.String()
}

// benchWallModel builds an organized overview model that mirrors a busy
// cockpit wall: live agents, delivered-idle agents, failed agents, runtime
// cards, services, and shells, all with realistic ANSI-heavy previews.
func benchWallModel(tb testing.TB, agents, runtimes int) *Model {
	tb.Helper()
	zone.DefaultManager = zone.New()
	m := NewModel(nil, time.Second, 6, nil, false, true)
	m.SetOrganized(true)
	m.width = 220
	m.height = 60
	m.preferredCols = 4
	m.lastUpdated = time.Now()

	liveTail := "\x1b[38;5;108m* working...\x1b[0m esc to interrupt\n"
	idleTail := "worked for 12m 3s\n| > \n"
	failTail := "Error: exit status 1\nFAILED\n"

	for i := 0; i < agents; i++ {
		var s tmux.Session
		switch i % 3 {
		case 0:
			s = agentSessionForGroup(fmt.Sprintf("agent-live-%02d", i), "running")
			s.Windows[0].Panes[0].PreviewText = benchPaneBody(200, liveTail)
		case 1:
			s = agentSessionForGroup(fmt.Sprintf("agent-idle-%02d", i), "running")
			s.Windows[0].Panes[0].PreviewText = benchPaneBody(200, idleTail)
		default:
			s = agentSessionForGroup(fmt.Sprintf("agent-fail-%02d", i), "running")
			s.Windows[0].Panes[0].PreviewText = benchPaneBody(200, failTail)
		}
		m.sessions = append(m.sessions, s)
	}
	for i := 0; i < runtimes; i++ {
		group := "route_health"
		if i%2 == 0 {
			group = "needs_decision"
		}
		s := sessionForGroup(fmt.Sprintf("runtime-%02d", i), "openclaw-runtime", "OpenClaw Runtime", "route")
		s.Windows[0].Panes[0].Cockpit = testRuntimeMeta(group)
		s.Windows[0].Panes[0].PreviewText = benchPaneBody(30, "status: review\n")
		m.sessions = append(m.sessions, s)
	}
	for i := 0; i < 4; i++ {
		s := sessionForGroup(fmt.Sprintf("svc-go2rtc-%02d", i), "go2rtc", "/opt/svc", "camera bridge")
		s.Windows[0].Panes[0].PreviewText = benchPaneBody(60, "listening on :1984\n")
		m.sessions = append(m.sessions, s)
	}
	for i := 0; i < 2; i++ {
		s := sessionForGroup(fmt.Sprintf("shell-%02d", i), "zsh", "/Users/cass", "")
		m.sessions = append(m.sessions, s)
	}

	for _, s := range m.sessions {
		vp := viewportFor(innerDimension{width: 48, height: 8})
		vp.SetContent(cardSafeBlock(strings.TrimRight(s.Windows[0].Panes[0].PreviewText, "\n")))
		vp.GotoBottom()
		m.previews[s.ID] = &sessionPreview{
			viewport:   &vp,
			paneID:     s.Windows[0].Panes[0].ID,
			autoFollow: true,
		}
	}

	// Mirror the snapshotMsg refresh sequence (no evidence paths => no disk IO).
	m.refreshArtifactOutcomes()
	m.refreshLifecycleVerdicts()
	m.updateStaleSessions()
	return m
}

// BenchmarkViewOrganizedWall measures the full frame path — classification,
// card render, windowing, and zone.Scan — the cost of every DIRTY frame.
// Each iteration force-dirties the F1 frame cache so this stays a full-rebuild
// baseline comparable with pre-cache runs; it deliberately does not measure
// cache hits (see BenchmarkViewMessageMixStreaming / BenchmarkViewNoopFastTicks).
func BenchmarkViewOrganizedWall(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.markRenderDirty()
		_ = m.View()
	}
}

// BenchmarkViewCollapsedGroups measures the same frame with most accordions
// collapsed, which additionally exercises per-group collapsed summaries.
func BenchmarkViewCollapsedGroups(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	_ = m.View() // seed groups
	for _, g := range orderedCockpitGroups(m, m.filteredSessions()) {
		if g.name != groupActiveAgents.name {
			m.collapsedGroups[g.name] = struct{}{}
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.markRenderDirty()
		_ = m.View()
	}
}

// BenchmarkFilteredSessionsOrganized measures the filter+classify+sort
// pipeline that runs several times per frame and once per input event.
func BenchmarkFilteredSessionsOrganized(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.filteredSessions()
	}
}

// BenchmarkFilteredSessionsColdCache measures the filter+classify+sort
// pipeline from an empty classification cache — the cost a global
// invalidation forces onto the next frame, which per-session invalidation
// (F6) avoids for unaffected sessions.
func BenchmarkFilteredSessionsColdCache(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.invalidateClassifications()
		_ = m.filteredSessions()
	}
}

// BenchmarkSessionAttentionStateHot measures one classification of a single
// content-heavy live agent session, the unit cost multiplied everywhere.
func BenchmarkSessionAttentionStateHot(b *testing.B) {
	m := benchWallModel(b, 3, 0)
	session := m.sessions[0]
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = sessionAttentionState(m, session)
	}
}

// BenchmarkViewOverviewWall measures a full View at fixed wall sizes. Each
// sub-benchmark's session total counts agents plus the fixed service/shell
// extras from benchWallModel, giving the per-message frame budget at 8, 16,
// and 32 sessions.
func BenchmarkViewOverviewWall(b *testing.B) {
	for _, total := range []int{8, 16, 32} {
		agents := total - 6 // benchWallModel adds 4 services + 2 shells
		if agents < 1 {
			agents = 1
		}
		b.Run(fmt.Sprintf("sessions-%d", total), func(b *testing.B) {
			m := benchWallModel(b, agents, 0)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.markRenderDirty()
				_ = m.View()
			}
		})
	}
}

// BenchmarkFastCaptureSignalSweep measures one watcher sweep over ten clean
// pane-log signals — the cost the off-loop watcher pays every sweep interval.
func BenchmarkFastCaptureSignalSweep(b *testing.B) {
	dir := b.TempDir()
	signals := make([]fastCaptureSignal, 10)
	for i := range signals {
		path := filepath.Join(dir, fmt.Sprintf("pane-%02d.log", i))
		if err := os.WriteFile(path, []byte("ready\n"), 0o644); err != nil {
			b.Fatalf("write log: %v", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			b.Fatalf("stat log: %v", err)
		}
		signals[i] = fastCaptureSignal{
			sessionID: fmt.Sprintf("$s%02d", i),
			path:      path,
			size:      info.Size(),
			modTime:   info.ModTime(),
			seen:      true,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if fastCaptureSignalsDirty(signals) {
			b.Fatal("clean signals reported dirty")
		}
	}
}

// benchStreamingTraffic replays one simulated second of streaming lane
// traffic through Update and View: sixty fastTickMsg heartbeats (each
// followed by a render, as Bubble Tea does) plus one changed-content
// paneContentMsg. forceDirty replicates pre-F1 behavior where every message
// rebuilt the frame, giving an in-tree before/after for the cache.
func benchStreamingTraffic(b *testing.B, forceDirty bool) {
	m := benchWallModel(b, 30, 8)
	_ = m.View()
	target := m.sessions[0]
	paneID := target.Windows[0].Panes[0].ID
	body := benchPaneBody(200, "\x1b[38;5;108m* working...\x1b[0m esc to interrupt\n")
	startBuilds := m.renderBuilds
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for tick := 0; tick < 60; tick++ {
			m.Update(fastTickMsg{})
			if forceDirty {
				m.markRenderDirty()
			}
			_ = m.View()
		}
		m.Update(paneContentMsg{
			sessionID: target.ID,
			paneID:    paneID,
			text:      fmt.Sprintf("%sprogress update %d\n", body, i),
		})
		if forceDirty {
			m.markRenderDirty()
		}
		_ = m.View()
	}
	b.StopTimer()
	b.ReportMetric(float64(m.renderBuilds-startBuilds)/float64(b.N), "builds/op")
}

// BenchmarkViewMessageMixStreaming is the F1 acceptance benchmark: repeated
// render-neutral heartbeat ticks plus one dirty capture per simulated second.
// builds/op shows how many full frame rebuilds the mix actually paid.
func BenchmarkViewMessageMixStreaming(b *testing.B) {
	benchStreamingTraffic(b, false)
}

// BenchmarkViewMessageMixForcedDirty replays the identical traffic with the
// frame cache defeated (every message force-dirtied) — the pre-F1 cost of the
// same message mix, measured by the same code.
func BenchmarkViewMessageMixForcedDirty(b *testing.B) {
	benchStreamingTraffic(b, true)
}

// BenchmarkViewNoopFastTicks measures the pure clean-heartbeat path: a
// fastTickMsg through Update plus a cached View. builds/op near zero is the
// no-op proof at benchmark scale; the strict assertion lives in
// TestFastTickMessageMixDoesNotRebuild with a frozen clock.
func BenchmarkViewNoopFastTicks(b *testing.B) {
	m := benchWallModel(b, 30, 8)
	_ = m.View()
	startBuilds := m.renderBuilds
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m.Update(fastTickMsg{})
		_ = m.View()
	}
	b.StopTimer()
	b.ReportMetric(float64(m.renderBuilds-startBuilds)/float64(b.N), "builds/op")
}

// BenchmarkPaneLifecycleVerdict bounds the per-capture classification cost on
// a 600-line ANSI-heavy agent transcript (the refreshPaneLifecycleVerdict
// path that runs once per changed pane capture).
func BenchmarkPaneLifecycleVerdict(b *testing.B) {
	pane := tmux.Pane{
		ID:          "%bench",
		Active:      true,
		PreviewText: benchPaneBody(600, "\x1b[38;5;108m* working...\x1b[0m esc to interrupt\n"),
		Cockpit:     &tmux.CockpitMeta{Kind: "agent", Agent: "fable", State: "running"},
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		verdict := paneLifecycleVerdictFor(pane, true)
		if verdict.state == "" {
			b.Fatal("expected a classified verdict")
		}
	}
}
