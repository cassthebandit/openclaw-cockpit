package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
	"github.com/cassthebandit/openclaw-cockpit/internal/zone"
)

func TestMain(m *testing.M) {
	zone.NewGlobal()
	os.Exit(m.Run())
}

func TestCockpitGroupForCurrentFleetShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session tmux.Session
		want    string
	}{
		{
			name: "fable session is a live active agent",
			session: sessionForGroup("clean-draft-fable-extract", "claude.exe",
				"/Users/cass/projects/clean-draft", "Implement V1 extract job artifacts"),
			want: groupActiveAgents.name,
		},
		{
			name: "camera service stays service",
			session: sessionForGroup("camera-rtsp", "go2rtc",
				"/Users/cass/.openclaw/workspace/config/camera-rtsp", ""),
			want: groupServices.name,
		},
		{
			name: "pantry claude frontend stays service",
			session: sessionForGroup("pantry-copilot-claude-front", "node",
				"/Users/cass/.openclaw/workspace/projects/pantry-copilot-claude-front", ""),
			want: groupServices.name,
		},
		{
			name: "wall session is dashboard",
			session: sessionForGroup("cass-agents", "tmuxwatch-cass",
				"/Users/cass/.openclaw/workspace", ""),
			want: groupDashboard.name,
		},
		{
			name: "html helper is viewer",
			session: sessionForGroup("clean-draft-daniel-brief-html", "Python",
				"/Users/cass/.openclaw/workspace/memory/runs/fable-daniel-brief-build", ""),
			want: groupViewers.name,
		},
		{
			name: "explicit detected viewer metadata is viewer",
			session: func() tmux.Session {
				session := sessionForGroup("maybrie-paris-site", "Python",
					"/Users/cass/.openclaw/workspace/projects/paris-family-2026", "")
				session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
					Kind:  "detected-viewer",
					Agent: "",
					State: "running",
				}
				return session
			}(),
			want: groupViewers.name,
		},
		{
			name:    "plain shell is idle",
			session: sessionForGroup("scratch", "zsh", "/tmp", ""),
			want:    groupIdle.name,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := cockpitGroupFor(nil, tt.session).name; got != tt.want {
				t.Fatalf("cockpitGroupFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSortSessionsForCockpit(t *testing.T) {
	t.Parallel()

	now := time.Now()
	sessions := []tmux.Session{
		withActivity(sessionForGroup("camera-rtsp", "go2rtc", "/camera", ""), now.Add(-time.Minute)),
		withActivity(sessionForGroup("cass-agents", "tmuxwatch-cass", "/workspace", ""), now),
		withActivity(sessionForGroup("committee-specb-codex", "zsh", "/workspace", ""), now.Add(-5*time.Minute)),
		withActivity(sessionForGroup("clean-draft-daniel-brief-html", "Python", "/memory/runs/daniel-brief", ""), now.Add(-2*time.Minute)),
		withActivity(sessionForGroup("scratch", "zsh", "/tmp", ""), now.Add(-30*time.Second)),
	}

	sortSessionsForCockpit(nil, sessions)

	got := []string{}
	for _, session := range sessions {
		got = append(got, session.Name)
	}
	want := []string{
		"committee-specb-codex",
		"camera-rtsp",
		"cass-agents",
		"clean-draft-daniel-brief-html",
		"scratch",
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted[%d] = %q, want %q; full order=%v", i, got[i], want[i], got)
		}
	}
}

func TestCockpitGroupUsesSessionAttentionRollup(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("worker", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		Kind:  "agent",
		Agent: "codex",
		State: "running",
	}
	session.Windows[0].Panes = append(session.Windows[0].Panes, tmux.Pane{
		ID:          "%hidden-failed",
		CurrentCmd:  "zsh",
		CurrentPath: "/workspace",
		Dead:        true,
		DeadStatus:  7,
		Cockpit: &tmux.CockpitMeta{
			Kind:  "agent",
			Agent: "codex",
			State: "running",
		},
	})

	if got := sessionAttentionState(nil, session); got != "failed" {
		t.Fatalf("sessionAttentionState() = %q, want failed", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupFailedAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupFailedAgents.name)
	}
}

func TestCockpitGroupKeepsLiveHeldAgentActive(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("held-but-running", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		Kind:          "agent",
		Agent:         "codex",
		State:         "running",
		HoldReason:    "manual review",
		CleanupPolicy: "manual",
	}

	if got := sessionAttentionState(nil, session); got != "running" {
		t.Fatalf("sessionAttentionState() = %q, want running", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
}

func TestDisplayOnlyMetadataDoesNotOverrideDeadPane(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("adopted-done", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].Dead = true
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "display-only",
		ManagedBy:       "manual_adopt",
		Kind:            "agent",
		Agent:           "claude",
		State:           "running",
	}

	if got := sessionAttentionState(nil, session); got != "done" {
		t.Fatalf("sessionAttentionState() = %q, want done", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
	}
}

func TestManagedDeadRunningPaneDowngradesToStale(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("managed-dead", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].Dead = true
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "1",
		ManagedBy:       "agent_wall",
		Kind:            "agent",
		Agent:           "codex",
		State:           "running",
	}

	if got := sessionAttentionState(nil, session); got != "stale" {
		t.Fatalf("sessionAttentionState() = %q, want stale", got)
	}
}

func TestCleanNullArtifactDoesNotDowngradeDeadNonzeroPane(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resultDir := filepath.Join(root, "results", "spark_smoke")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "verification.json"), []byte(`{
  "status": "PASS",
  "result_classification": "LIVE_MICROARM_NULL_SAFE",
  "errors": []
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session := sessionForGroup("loop-v12-spark-smoke-rerun1", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Dead = true
	pane.DeadStatus = 1
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:          "batch-worker",
		Agent:         "codex-spark",
		State:         "failed",
		RunRoot:       root,
		EvidencePath:  "results/spark_smoke/RESULT.md",
		EndReason:     "process_exit_nonzero",
		CleanupPolicy: "kill_on_done",
	}
	if err := os.WriteFile(filepath.Join(resultDir, "RESULT.md"), []byte("classification: `LIVE_MICROARM_NULL_SAFE`\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if got := sessionAttentionState(nil, session); got != "failed" {
		t.Fatalf("sessionAttentionState() = %q, want failed", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupFailedAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupFailedAgents.name)
	}
}

func TestArtifactOutcomeUsesNamedEvidenceFileBeforeSiblingJSON(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resultDir := filepath.Join(root, "results", "spark_smoke")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "verification.json"), []byte(`{
  "status": "PASS",
  "result_classification": "LIVE_MICROARM_NULL_SAFE"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "RESULT.md"), []byte("classification: failed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	session := sessionForGroup("worker", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:         "batch-worker",
		Agent:        "codex",
		State:        "done",
		RunRoot:      root,
		EvidencePath: "results/spark_smoke/RESULT.md",
	}

	if got := sessionAttentionState(nil, session); got != "failed" {
		t.Fatalf("sessionAttentionState() = %q, want failed", got)
	}
}

func TestArtifactOutcomeCanReadExplicitEvidenceDirectory(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	resultDir := filepath.Join(root, "results", "spark_smoke")
	if err := os.MkdirAll(resultDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(resultDir, "verification.json"), []byte(`{
  "status": "PASS",
  "result_classification": "LIVE_MICROARM_NULL_SAFE"
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	session := sessionForGroup("worker", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:         "batch-worker",
		Agent:        "codex",
		State:        "done",
		RunRoot:      root,
		EvidencePath: "results/spark_smoke",
	}

	if got := sessionAttentionState(nil, session); got != "null-safe" {
		t.Fatalf("sessionAttentionState() = %q, want null-safe", got)
	}
}

func TestMissingDeclaredEvidencePathNeedsReview(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := sessionForGroup("worker", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:         "batch-worker",
		Agent:        "codex",
		State:        "done",
		RunRoot:      root,
		EvidencePath: "results/missing/RESULT.md",
	}

	if got := sessionAttentionState(nil, session); got != "review" {
		t.Fatalf("sessionAttentionState() = %q, want review", got)
	}
	// A live (non-dead) managed agent in a review sub-state stays in the live
	// Active Agents band per the committee taxonomy (§9.2).
	if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
}

func TestModelAttentionStateUsesCachedArtifactOutcome(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := sessionForGroup("worker", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:         "batch-worker",
		Agent:        "codex",
		State:        "done",
		RunRoot:      root,
		EvidencePath: "results/missing/RESULT.md",
	}

	m := &Model{artifactOutcomes: map[string]string{artifactOutcomeCacheKey(*pane): "pass"}}
	if got := paneAttentionState(m, session, *pane); got != "pass" {
		t.Fatalf("paneAttentionState() = %q, want cached pass", got)
	}

	m.artifactOutcomes = map[string]string{}
	if got := paneAttentionState(m, session, *pane); got != "done" {
		t.Fatalf("paneAttentionState without cache = %q, want metadata state without filesystem read", got)
	}
}

func TestActiveScreenVetoesCachedArtifactPass(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	session := sessionForGroup("reused-agent-pane", "run.sh", root, "spark")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:         "batch-worker",
		Agent:        "codex",
		State:        "done",
		RunRoot:      root,
		EvidencePath: "results/old/RESULT.md",
	}
	pane.PreviewText = "still running\nEsc to interrupt\n› "

	m := &Model{artifactOutcomes: map[string]string{artifactOutcomeCacheKey(*pane): "pass"}}
	if got := paneAttentionState(m, session, *pane); got != "running" {
		t.Fatalf("paneAttentionState() = %q, want running", got)
	}
}

func TestLifecycleUsesCapturedPreviewWhenPanePreviewTextIsEmpty(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("captured-preview-agent", "running")
	pane := &session.Windows[0].Panes[0]
	pane.PreviewText = ""
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.sessions = []tmux.Session{session}
	m.previews[session.ID] = &sessionPreview{paneID: pane.ID, lastContent: fableIdleFinished}
	m.refreshLifecycleVerdicts()

	if got := paneAttentionState(m, session, *pane); got != "delivered-idle" {
		t.Fatalf("paneAttentionState() = %q, want delivered-idle", got)
	}
}

func TestMetadataOnlyWaitingStaysInteractive(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("metadata-waiting", "waiting")
	if got := sessionAttentionState(nil, session); got != "waiting" {
		t.Fatalf("sessionAttentionState() = %q, want waiting", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
}

func TestManagedDeliveredIdleIgnoresSiblingShellPane(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("agent-with-helper-shell", "running")
	session.Windows[0].Panes[0].PreviewText = fableIdleFinished
	session.Windows[0].Panes = append(session.Windows[0].Panes, tmux.Pane{
		ID:          "%helper",
		CurrentCmd:  "zsh",
		CurrentPath: "/workspace",
		PreviewText: "helper shell\n❯ ",
	})

	if got := sessionAttentionState(nil, session); got != "delivered-idle" {
		t.Fatalf("sessionAttentionState() = %q, want delivered-idle", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
	}
}

func TestUnmanagedAgentLikeDoneInShellDoesNotComplete(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("codex-docs", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].PreviewText = "Done in 2.34s\n❯ "

	if got := sessionAttentionState(nil, session); got != "running" {
		t.Fatalf("sessionAttentionState() = %q, want running", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
}

func TestClassificationStateRequiresExactToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value string
		want  string
	}{
		{value: "LIVE_MICROARM_NULL_SAFE", want: "null-safe"},
		{value: "`signal_ok`", want: "signal"},
		{value: "classification: directional", want: "directional"},
		{value: "result_classification = degraded", want: "review"},
		{value: "not_failure", want: ""},
		{value: "non-null signal", want: ""},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.value, func(t *testing.T) {
			t.Parallel()

			if got := classificationState(extractClassificationValue(tt.value)); got != tt.want {
				t.Fatalf("classificationState(%q) = %q, want %q", tt.value, got, tt.want)
			}
		})
	}
}

func TestRawDeadNonzeroManagedPaneRemainsAttention(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("failed-worker", "run.sh", "/workspace", "worker")
	pane := &session.Windows[0].Panes[0]
	pane.Dead = true
	pane.DeadStatus = 1
	pane.Cockpit = &tmux.CockpitMeta{
		Kind:          "batch-worker",
		Agent:         "codex",
		State:         "failed",
		EndReason:     "process_exit_nonzero",
		CleanupPolicy: "kill_on_done",
	}

	if got := sessionAttentionState(nil, session); got != "failed" {
		t.Fatalf("sessionAttentionState() = %q, want failed", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupFailedAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupFailedAgents.name)
	}
}

func TestDisplayOnlyMetadataDoesNotOverrideStalePane(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("adopted-stale", "zsh", "/workspace", "")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "display-only",
		ManagedBy:       "manual_adopt",
		Kind:            "agent",
		Agent:           "claude",
		State:           "running",
	}
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.stale[session.ID] = struct{}{}

	if got := sessionAttentionState(m, session); got != "stale" {
		t.Fatalf("sessionAttentionState() = %q, want stale", got)
	}
}

func TestOpenClawRuntimeDisplayGroupRoutesNeedsAttentionEvenWhenStale(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("openclaw-runtime-flow-1", "openclaw-runtime", "/workspace", "backend packet")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "runtime-card.v1",
		ManagedBy:       "openclaw_runtime_snapshot",
		Kind:            "runtime",
		Agent:           "taskflow",
		State:           "blocked",
		DisplayStatus:   "blocked",
		DisplayGroup:    "needs_attention",
	}
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.stale[session.ID] = struct{}{}

	if got := sessionAttentionState(m, session); got != "blocked" {
		t.Fatalf("sessionAttentionState() = %q, want blocked", got)
	}
	// needs_attention with no presentationGroup + a decision-like (blocked)
	// state falls back to Operational Failures.
	if got := cockpitGroupFor(m, session).name; got != groupOperationalFailures.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupOperationalFailures.name)
	}
}

func TestOpenClawRuntimeActiveGroupUsesDisplayGroup(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("openclaw-runtime-flow-2", "openclaw-runtime", "/workspace", "live taskflow")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "runtime-card.v1",
		ManagedBy:       "openclaw_runtime_snapshot",
		Kind:            "runtime",
		Agent:           "taskflow",
		State:           "running",
		DisplayStatus:   "active",
		DisplayGroup:    "active",
	}

	if got := cockpitGroupFor(nil, session).name; got != groupOperationalFailures.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupOperationalFailures.name)
	}
}

func TestOpenClawRuntimePresentationGroupRoutesRuntimeCards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		presentationGroup string
		want              string
	}{
		{name: "current work", presentationGroup: "current_work", want: groupOperationalFailures.name},
		{name: "needs decision", presentationGroup: "needs_decision", want: groupOperationalFailures.name},
		{name: "route health", presentationGroup: "route_health", want: groupSubsystemFailures.name},
		{name: "delivery handoff", presentationGroup: "delivery_handoff", want: groupOperationalFailures.name},
		{name: "source unknown", presentationGroup: "source_unknown", want: groupSubsystemFailures.name},
		{name: "expected controls", presentationGroup: "expected_controls", want: groupSubsystemFailures.name},
		{name: "skeletons", presentationGroup: "skeletons", want: groupSubsystemFailures.name},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			session := sessionForGroup("openclaw-runtime-"+tt.name, "openclaw-runtime", "OpenClaw Runtime", "")
			session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
				ManagedBy:         "openclaw_runtime_snapshot",
				Kind:              "runtime",
				Agent:             "openclaw-runtime",
				State:             "review",
				DisplayGroup:      "needs_attention",
				PresentationGroup: tt.presentationGroup,
			}

			if got := cockpitGroupFor(nil, session).name; got != tt.want {
				t.Fatalf("cockpitGroupFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuietServiceStaleDoesNotBecomeDoneHeld(t *testing.T) {
	t.Parallel()

	session := sessionForGroup("AI-Alerts", "Python", "/workspace/config/smonitor", "AI-Alerts")
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.stale[session.ID] = struct{}{}

	if got := sessionAttentionState(m, session); got != "quiet" {
		t.Fatalf("sessionAttentionState() = %q, want quiet", got)
	}
	if got := cockpitGroupFor(m, session).name; got != groupServices.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupServices.name)
	}
}

func TestCardLayoutForCountUsesGroupWidth(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 90
	m.preferredCols = 5
	m.cardInnerWidth = 68

	cols, inner := m.cardLayoutForCount(3)
	if cols != 3 {
		t.Fatalf("cols = %d, want 3", cols)
	}
	if inner <= 100 {
		t.Fatalf("inner width = %d, want wide third-row cards", inner)
	}
}

func TestCardLayoutForCountReservesColumnGutters(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.preferredCols = 4

	cols, inner := m.cardLayoutForCount(20)
	cellWidth := inner + cardPadding*2 + 2
	rowWidth := cols*cellWidth + (cols-1)*cardColumnGap

	if cols != 4 {
		t.Fatalf("cols = %d, want 4", cols)
	}
	if rowWidth > m.width {
		t.Fatalf("row width = %d, terminal width = %d", rowWidth, m.width)
	}
}

func TestRuntimeGroupLayoutCanOverrideLiveColsCap(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 90
	m.preferredCols = 4

	globalCols, _ := m.cardLayoutForCount(10)
	if globalCols != 4 {
		t.Fatalf("global cols = %d, want live --cols cap of 4", globalCols)
	}

	runtimeCols, runtimeInner := m.cardLayoutForGroup(groupOperationalFailures, 10)
	if runtimeCols != 5 {
		t.Fatalf("runtime cols = %d, want 5 despite live --cols cap", runtimeCols)
	}
	if runtimeInner < 30 {
		t.Fatalf("runtime inner width = %d, want readable 5-across width", runtimeInner)
	}
}

func TestRuntimeGroupLayoutRejectsCrampedFiveAcross(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 150
	m.height = 50
	m.preferredCols = 4

	cols, inner := m.cardLayoutForGroup(groupOperationalFailures, 10)
	if cols >= 5 {
		t.Fatalf("runtime cols = %d, want fewer than 5 on mid-width terminal", cols)
	}
	if inner < 30 && cols > 1 {
		t.Fatalf("inner width = %d, want readable runtime width", inner)
	}
}

func TestRuntimeGroupHeightBudgetUsesGroupLayout(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 90
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	for i := 0; i < 10; i++ {
		session := sessionForGroup(fmt.Sprintf("route-health-%02d", i), "openclaw-runtime", "OpenClaw Runtime", "route")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		m.sessions = append(m.sessions, session)
	}

	cols, _ := m.cardLayoutForGroup(groupSubsystemFailures, len(m.sessions))
	if cols != 5 {
		t.Fatalf("runtime render cols = %d, want 5", cols)
	}
	rows := (len(m.sessions) + cols - 1) / cols
	if rows != 2 {
		t.Fatalf("runtime rows = %d, want 2 rows for 10 cards at 5-across", rows)
	}
	heights := m.cardBodyHeightsByGroup(m.sessions)
	if heights[groupSubsystemFailures.name] <= 0 {
		t.Fatalf("missing runtime body budget: %#v", heights)
	}
}

func TestRuntimeGroupCursorMovementStaysOnVisibleCard(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 90
	m.preferredCols = 4
	m.cardCols = 4
	for i := 0; i < 5; i++ {
		session := sessionForGroup(fmt.Sprintf("route-health-%02d", i), "openclaw-runtime", "OpenClaw Runtime", "route")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		m.sessions = append(m.sessions, session)
	}
	for i := 0; i < 3; i++ {
		session := sessionForGroup(fmt.Sprintf("current-work-%02d", i), "openclaw-runtime", "OpenClaw Runtime", "work")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("current_work")
		m.sessions = append(m.sessions, session)
	}

	visible := func() map[string]struct{} {
		sessions := m.filteredSessions()
		ids := make(map[string]struct{}, len(sessions))
		for _, session := range sessions {
			ids[session.ID] = struct{}{}
		}
		return ids
	}
	assertVisible := func(action string) {
		if _, ok := visible()[m.cursorSession]; !ok {
			t.Fatalf("cursor after %s = %q, want visible session", action, m.cursorSession)
		}
	}

	m.cursorSession = m.filteredSessions()[0].ID
	for i := 0; i < 12; i++ {
		m.moveCursorRight()
		assertVisible("right")
		m.moveCursorDown()
		assertVisible("down")
		m.moveCursorLeft()
		assertVisible("left")
		m.moveCursorUp()
		assertVisible("up")
	}
}

func TestOrganizedCardBodyHeightsUseVerticalSpace(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 5
	m.cardInnerWidth = 68
	m.sessions = []tmux.Session{
		sessionForGroup("clean-draft-tranche3-fable-code", "claude", "/workspace", "Implement guardrails"),
		sessionForGroup("AI-Alerts", "Python", "/workspace/config/smonitor", "AI-Alerts"),
		sessionForGroup("s-apple-detector", "Python", "/workspace/config/camera", "s-apple-detector"),
		sessionForGroup("smonitor", "go2rtc", "/workspace/config/smonitor", "smonitor"),
		sessionForGroup("clean-draft-tranche4-fable-code", "claude", "/workspace", "Finished tranche"),
	}
	m.sessions[4].Windows[0].Panes[0].Dead = true
	// Actually janitor-marked: only marked sessions render under Marked For
	// Teardown since the registry reset.
	m.sessions[4].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		Kind: "agent", Agent: "claude", State: "done",
		TeardownMarkedAt: "2026-07-08T01:26:00Z", JanitorState: "marked_for_teardown",
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	running := heights[groupActiveAgents.name]
	services := heights[groupServices.name]
	done := heights[groupInactiveAgents.name]

	if running <= maxOverviewBodyLines {
		t.Fatalf("active agent height = %d, want more than old fixed cap %d", running, maxOverviewBodyLines)
	}
	if services <= 0 || services > 8 {
		t.Fatalf("service height = %d, want compact service budget", services)
	}
	if done <= 7 {
		t.Fatalf("inactive agent height = %d, want inspectable inactive budget", done)
	}
}

func TestOrganizedCardBodyHeightsRelaxTopOpenGroupCap(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardInnerWidth = 86
	m.sessions = []tmux.Session{
		sessionForGroup("workshop-fable-live", "claude", "/workspace", "Implement dynamic accordion layout"),
	}
	vp := viewportFor(innerDimension{width: 86, height: 6})
	vp.SetContent("live work\nmore work\nreviewing allocator")
	m.previews[m.sessions[0].ID] = &sessionPreview{viewport: &vp, paneID: m.sessions[0].Windows[0].Panes[0].ID}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	active := heights[groupActiveAgents.name]
	if active <= bodyHeightConstraintForGroup(groupActiveAgents).max {
		t.Fatalf("active height = %d, want top open group to relax past soft cap", active)
	}

	view := stripANSI(m.renderSessionPreviews(m.previewOffset))
	available := m.previewAvailableHeight()
	if got := countLines(view); got < available-1 {
		t.Fatalf("rendered organized view uses %d lines, want near available %d; view:\n%s", got, available, view)
	}
}

func TestOrganizedCardBodyHeightsCollapsedTopPromotesNextOpenGroup(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardInnerWidth = 86
	m.sessions = []tmux.Session{
		sessionForGroup("workshop-fable-live", "claude", "/workspace", "Implement dynamic accordion layout"),
		sessionForGroup("workshop-codex-done", "codex", "/workspace", "Finished"),
	}
	m.sessions[1].Windows[0].Panes[0].Dead = true
	m.sessions[1].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		Kind: "agent", Agent: "codex", State: "done",
		TeardownMarkedAt: "2026-07-08T01:26:00Z", JanitorState: "marked_for_teardown",
	}
	m.toggleGroupCollapsed(groupActiveAgents.name)

	heights := m.cardBodyHeightsByGroup(m.sessions)
	if _, ok := heights[groupActiveAgents.name]; ok {
		t.Fatalf("collapsed active group should not receive body height: %#v", heights)
	}
	inactive := heights[groupInactiveAgents.name]
	if inactive <= bodyHeightConstraintForGroup(groupInactiveAgents).max {
		t.Fatalf("inactive height = %d, want first expanded non-empty group to relax past soft cap", inactive)
	}
}

func TestOrganizedCardBodyHeightsKeepLowerServicesCompact(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardInnerWidth = 86
	m.sessions = []tmux.Session{
		sessionForGroup("workshop-fable-live", "claude", "/workspace", "Implement dynamic accordion layout"),
		sessionForGroup("smonitor", "go2rtc", "/workspace/config/smonitor", "smonitor"),
	}
	m.sessions[1].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "display-only",
		ManagedBy:       "manual_adopt",
		Kind:            "service",
		Agent:           "service",
		State:           "running",
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	active := heights[groupActiveAgents.name]
	services := heights[groupServices.name]
	if active <= services {
		t.Fatalf("active height = %d, services height = %d; want top group larger", active, services)
	}
	if services <= 0 || services > bodyHeightConstraintForGroup(groupServices).max {
		t.Fatalf("services height = %d, want compact positive budget no larger than %d", services, bodyHeightConstraintForGroup(groupServices).max)
	}
}

func TestOrganizedCardBodyHeightsLiveShapeSpendsCollapsedGroupSpace(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 2
	m.footerHeight = 3
	m.preferredCols = 4
	m.cardInnerWidth = 86
	m.sessions = []tmux.Session{
		sessionForGroup("dynamic-fable-review", "claude", "/workspace", "Review layout"),
		sessionForGroup("other-fable-review", "claude", "/workspace", "Review structure"),
	}
	for _, session := range m.sessions {
		vp := viewportFor(innerDimension{width: 170, height: 20})
		vp.SetContent(strings.Join([]string{
			"reviewing allocator",
			"reading tests",
			"checking live shape",
		}, "\n"))
		m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: session.Windows[0].Panes[0].ID}
	}
	for i := 0; i < 5; i++ {
		session := sessionForGroup(fmt.Sprintf("route-health-%02d", i), "openclaw-runtime", "/workspace", "route")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		vp := viewportFor(innerDimension{width: 68, height: 6})
		vp.SetContent("route health\nsource unknown")
		m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: session.Windows[0].Panes[0].ID}
		m.sessions = append(m.sessions, session)
	}
	for i := 0; i < 3; i++ {
		session := sessionForGroup(fmt.Sprintf("service-%02d", i), "go2rtc", "/workspace/config/smonitor", "service")
		session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
			ContractVersion: "display-only",
			ManagedBy:       "manual_adopt",
			Kind:            "service",
			Agent:           "service",
			State:           "running",
		}
		m.sessions = append(m.sessions, session)
	}
	m.toggleGroupCollapsed(groupOperationalFailures.name)
	m.toggleGroupCollapsed(groupServices.name)

	heights := m.cardBodyHeightsByGroup(m.sessions)
	active := heights[groupActiveAgents.name]
	subsystem := heights[groupSubsystemFailures.name]
	if active < bodyHeightConstraintForGroup(groupActiveAgents).max {
		t.Fatalf("active height = %d, want live top group to receive soft-cap budget; heights=%#v", active, heights)
	}
	if subsystem <= 0 || subsystem > bodyHeightConstraintForGroup(groupSubsystemFailures).max {
		t.Fatalf("sub-system height = %d, want compact positive budget; heights=%#v", subsystem, heights)
	}

	renderedRows := countLines(stripANSI(m.renderSessionPreviews(m.previewOffset)))
	if renderedRows < m.previewAvailableHeight()-1 {
		t.Fatalf("rendered rows = %d, want near available %d; heights=%#v", renderedRows, m.previewAvailableHeight(), heights)
	}
}

func TestOrganizedCardBodyHeightsKeepServicesCompactWhenAttentionCrowds(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 363
	m.height = 89
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardInnerWidth = 86

	for i := 0; i < 20; i++ {
		session := sessionForGroup(fmt.Sprintf("runtime-attention-%02d", i), "openclaw-runtime", "/workspace", "blocked task")
		session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
			ContractVersion: "runtime-card.v1",
			ManagedBy:       "openclaw_runtime_snapshot",
			Kind:            "runtime",
			Agent:           "taskflow",
			State:           "blocked",
			DisplayGroup:    "needs_attention",
		}
		m.sessions = append(m.sessions, session)
	}
	for _, name := range []string{"AI-Alerts", "s-apple-detector", "smonitor"} {
		session := sessionForGroup(name, "service", "/workspace/config/smonitor", name)
		session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
			ContractVersion: "display-only",
			ManagedBy:       "manual_adopt",
			Kind:            "service",
			Agent:           "service",
			State:           "running",
		}
		m.sessions = append(m.sessions, session)
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	services := heights[groupServices.name]

	if services <= 0 || services > 8 {
		t.Fatalf("service height = %d, want compact service budget", services)
	}
}

func TestOrganizedCardBodyHeightsShrinkSmallTerminalCleanly(t *testing.T) {
	t.Parallel()

	m := modelForAccordionSizing(100, 24)
	m.previewOffset = 4
	m.footerHeight = 4
	m.sessions = []tmux.Session{
		sessionForGroup("small-active", "claude", "/workspace", "Active work"),
		sessionForGroup("small-inactive", "codex", "/workspace", "Finished work"),
		sessionForGroup("small-runtime", "openclaw-runtime", "/workspace", "Runtime failure"),
		sessionForGroup("small-service", "service", "/workspace/config/smonitor", "Service"),
	}
	m.sessions[1].Windows[0].Panes[0].Dead = true
	m.sessions[1].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "agent", Agent: "codex", State: "done"}
	m.sessions[2].Windows[0].Panes[0].Cockpit = testRuntimeMeta("needs_attention")
	m.sessions[3].Windows[0].Panes[0].Cockpit = serviceMetaForTest()
	for _, session := range m.sessions {
		seedPreviewForSizingTest(m, session, 28, 8)
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	if len(heights) == 0 {
		t.Fatal("expected populated groups to receive at least one body row")
	}
	for group, height := range heights {
		if height < 1 {
			t.Fatalf("group %q height = %d, want at least 1", group, height)
		}
	}

	content := stripANSI(m.renderSessionPreviews(m.previewOffset))
	for _, want := range []string{groupActiveAgents.name, groupInactiveAgents.name, groupOperationalFailures.name, groupServices.name} {
		if !strings.Contains(content, want) {
			t.Fatalf("small terminal render missing divider %q in:\n%s", want, content)
		}
	}
	available := m.previewAvailableHeight()
	windowed := m.windowGrid(content, available)
	if got := countLines(windowed); got > available {
		t.Fatalf("windowed render lines = %d, want <= available %d; content:\n%s", got, available, content)
	}
	if countLines(content) > available && !m.pageScrollEngaged {
		t.Fatalf("raw content lines = %d exceed available %d, want page scroll engaged", countLines(content), available)
	}
}

func TestOrganizedCardBodyHeightsMultiRowAccountingStaysWithinBudget(t *testing.T) {
	t.Parallel()

	m := modelForAccordionSizing(363, 89)
	m.sessions = []tmux.Session{
		sessionForGroup("multi-active-1", "claude", "/workspace", "Active work"),
		sessionForGroup("multi-active-2", "claude", "/workspace", "More active work"),
	}
	for _, session := range m.sessions {
		seedPreviewForSizingTest(m, session, 86, 10)
	}
	for i := 0; i < 12; i++ {
		session := sessionForGroup(fmt.Sprintf("multi-runtime-%02d", i), "openclaw-runtime", "/workspace", "Route failure")
		session.Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
		seedPreviewForSizingTest(m, session, 68, 6)
		m.sessions = append(m.sessions, session)
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	counts := sessionGroupCounts(m, m.sessions)
	groups := orderedCockpitGroups(m, m.sessions)
	modeled := len(groups)
	multiRowSeen := false
	for _, group := range groups {
		height, ok := heights[group.name]
		if !ok || m.isGroupCollapsed(group.name) || counts[group.name] == 0 {
			continue
		}
		cols, _ := m.cardLayoutForGroup(group, counts[group.name])
		rows := (counts[group.name] + cols - 1) / cols
		if rows >= 2 {
			multiRowSeen = true
		}
		modeled += rows * (3 + height)
	}
	if !multiRowSeen {
		t.Fatal("test setup did not create a multi-row group")
	}
	if available := m.previewAvailableHeight(); modeled > available {
		t.Fatalf("modeled rows = %d, want <= available %d; heights=%#v", modeled, available, heights)
	}
}

func TestOrganizedCardBodyHeightsServicesOnlyCanUseScreen(t *testing.T) {
	t.Parallel()

	m := modelForAccordionSizing(363, 89)
	m.sessions = []tmux.Session{
		sessionForGroup("only-service", "service", "/workspace/config/smonitor", "Service"),
	}
	m.sessions[0].Windows[0].Panes[0].Cockpit = serviceMetaForTest()
	seedPreviewForSizingTest(m, m.sessions[0], 86, 10)

	services := m.cardBodyHeightsByGroup(m.sessions)[groupServices.name]
	if services <= bodyHeightConstraintForGroup(groupServices).max {
		t.Fatalf("services height = %d, want only populated open group to relax past cap %d", services, bodyHeightConstraintForGroup(groupServices).max)
	}
}

func TestOrganizedCardBodyHeightsAllGroupsCollapsedRenderDividersOnly(t *testing.T) {
	t.Parallel()

	m := modelForAccordionSizing(160, 40)
	m.sessions = []tmux.Session{
		sessionForGroup("collapsed-active", "claude", "/workspace", "Active work"),
		sessionForGroup("collapsed-service", "service", "/workspace/config/smonitor", "Service"),
	}
	m.sessions[1].Windows[0].Panes[0].Cockpit = serviceMetaForTest()
	for _, group := range orderedCockpitGroups(m, m.sessions) {
		m.toggleGroupCollapsed(group.name)
	}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	if len(heights) != 0 {
		t.Fatalf("all collapsed groups should receive no body heights, got %#v", heights)
	}
	view := stripANSI(m.renderSessionPreviews(m.previewOffset))
	if strings.Contains(view, "collapsed-active") || strings.Contains(view, "collapsed-service") {
		t.Fatalf("collapsed groups should render divider summaries only, got:\n%s", view)
	}
	for _, want := range []string{groupActiveAgents.name, groupServices.name} {
		if !strings.Contains(view, want) {
			t.Fatalf("collapsed render missing divider %q in:\n%s", want, view)
		}
	}
	if got, available := countLines(view), m.previewAvailableHeight(); got > available {
		t.Fatalf("collapsed divider render lines = %d, want <= available %d", got, available)
	}
}

func TestOrganizedCardBodyHeightsRenderWithinWindowAcrossGeometries(t *testing.T) {
	t.Parallel()

	for _, size := range []struct {
		width  int
		height int
	}{
		{width: 363, height: 89},
		{width: 200, height: 50},
		{width: 120, height: 30},
		{width: 80, height: 24},
	} {
		size := size
		t.Run(fmt.Sprintf("%dx%d", size.width, size.height), func(t *testing.T) {
			t.Parallel()

			m := modelForAccordionSizing(size.width, size.height)
			m.sessions = []tmux.Session{
				sessionForGroup("geometry-active", "claude", "/workspace", "Active work"),
				sessionForGroup("geometry-inactive", "codex", "/workspace", "Finished"),
				sessionForGroup("geometry-runtime", "openclaw-runtime", "/workspace", "Runtime failure"),
				sessionForGroup("geometry-service", "service", "/workspace/config/smonitor", "Service"),
			}
			m.sessions[1].Windows[0].Panes[0].Dead = true
			m.sessions[1].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "agent", Agent: "codex", State: "done"}
			m.sessions[2].Windows[0].Panes[0].Cockpit = testRuntimeMeta("route_health")
			m.sessions[3].Windows[0].Panes[0].Cockpit = serviceMetaForTest()
			for _, session := range m.sessions {
				seedPreviewForSizingTest(m, session, max(24, size.width/4), 10)
			}

			content := stripANSI(m.renderSessionPreviews(m.previewOffset))
			available := m.previewAvailableHeight()
			windowed := m.windowGrid(content, available)
			if got := countLines(windowed); got > available {
				t.Fatalf("windowed render lines = %d, want <= available %d; raw lines=%d", got, available, countLines(content))
			}
			if countLines(content) <= available && m.pageScrollEngaged {
				t.Fatalf("scroll engaged despite content lines %d fitting available %d", countLines(content), available)
			}
		})
	}
}

func modelForAccordionSizing(width, height int) *Model {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = width
	m.height = height
	m.previewOffset = 4
	m.footerHeight = 4
	m.preferredCols = 4
	m.cardInnerWidth = max(20, (width/4)-4)
	return m
}

func seedPreviewForSizingTest(m *Model, session tmux.Session, width, height int) {
	vp := viewportFor(innerDimension{width: width, height: height})
	vp.SetContent(strings.Join([]string{
		"line 01",
		"line 02",
		"line 03",
		"line 04",
		"line 05",
		"line 06",
		"line 07",
		"line 08",
		"line 09",
		"line 10",
	}, "\n"))
	m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: session.Windows[0].Panes[0].ID}
}

func serviceMetaForTest() *tmux.CockpitMeta {
	return &tmux.CockpitMeta{
		ContractVersion: "display-only",
		ManagedBy:       "manual_adopt",
		Kind:            "service",
		Agent:           "service",
		State:           "running",
	}
}

func TestStaleSessionNamesOmitsQuietLiveServices(t *testing.T) {
	t.Parallel()

	service := sessionForGroup("smonitor", "go2rtc", "/workspace/config/smonitor", "smonitor")
	worker := sessionForGroup("old-worker", "zsh", "/workspace", "")
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.sessions = []tmux.Session{service, worker}
	m.stale[service.ID] = struct{}{}
	m.stale[worker.ID] = struct{}{}

	got := strings.Join(m.staleSessionNames(), ",")
	if strings.Contains(got, "smonitor") {
		t.Fatalf("quiet service should not appear in stale footer, got %q", got)
	}
	if !strings.Contains(got, "old-worker") {
		t.Fatalf("stale worker should remain in footer, got %q", got)
	}
}

func TestStaleSessionNamesOmitsOpenClawRuntime(t *testing.T) {
	t.Parallel()

	runtime := sessionForGroup("openclaw-runtime-flow-1", "openclaw-runtime", "/workspace", "backend packet")
	runtime.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "runtime-card.v1",
		ManagedBy:       "openclaw_runtime_snapshot",
		Kind:            "runtime",
		Agent:           "taskflow",
		State:           "blocked",
		DisplayGroup:    "needs_attention",
	}
	worker := sessionForGroup("old-worker", "zsh", "/workspace", "")
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.sessions = []tmux.Session{runtime, worker}
	m.stale[runtime.ID] = struct{}{}
	m.stale[worker.ID] = struct{}{}

	got := strings.Join(m.staleSessionNames(), ",")
	if strings.Contains(got, "openclaw-runtime") {
		t.Fatalf("runtime should not appear in stale footer, got %q", got)
	}
	if !strings.Contains(got, "old-worker") {
		t.Fatalf("stale worker should remain in footer, got %q", got)
	}
}

func TestCockpitGroupRoutesWaitingBlockedAndDone(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state string
		want  string
	}{
		{state: "waiting", want: groupActiveAgents.name},
		{state: "blocked", want: groupActiveAgents.name},
		{state: "done", want: groupDoneHeld.name},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.state, func(t *testing.T) {
			t.Parallel()
			session := sessionForGroup("worker-"+tt.state, "zsh", "/workspace", "")
			session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "agent", Agent: "codex", State: tt.state}
			if got := cockpitGroupFor(nil, session).name; got != tt.want {
				t.Fatalf("cockpitGroupFor() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderSessionPreviewsAddsOrganizedDividers(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 160
	m.height = 40
	m.cardCols = 2
	m.cardInnerWidth = 70
	m.cardInnerHeight = 6
	m.sessions = []tmux.Session{
		sessionForGroup("committee-specb-codex", "zsh", "/workspace", ""),
		sessionForGroup("camera-rtsp", "go2rtc", "/camera", ""),
	}
	for _, session := range m.sessions {
		vp := viewportFor(innerDimension{width: 70, height: 6})
		vp.SetContent("content")
		m.previews[session.ID] = &sessionPreview{viewport: &vp, paneID: session.Windows[0].Panes[0].ID}
	}

	got := m.renderSessionPreviews(0)
	for _, want := range []string{groupActiveAgents.name, groupServices.name} {
		if !strings.Contains(got, want) {
			t.Fatalf("renderSessionPreviews missing divider %q in %q", want, got)
		}
	}
}

// agentSessionForGroup builds a managed agent session (kind=agent, via
// agent_wall) with the given @oc_state and a neutral name/command.
func agentSessionForGroup(name, state string) tmux.Session {
	session := sessionForGroup(name, "claude", "/Users/cass/projects/"+name, "")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "1",
		ManagedBy:       "agent_wall",
		Kind:            "agent",
		Agent:           "codex",
		State:           state,
	}
	return session
}

// modelWithJanitorSidecar returns a Model whose janitor status sidecar is
// fresh ("ok") and carries the given session rows.
func modelWithJanitorSidecar(rows map[string]janitorSessionStatus) *Model {
	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.janitorStatus = janitorStatusView{State: "ok", Sessions: rows}
	return m
}

// sidecarRowFor stamps a sidecar row with pane identity matching the session's
// first pane, mirroring what hygiene writes for a current pane.
func sidecarRowFor(session tmux.Session, row janitorSessionStatus) janitorSessionStatus {
	pane := session.Windows[0].Panes[0]
	row.PaneID = pane.ID
	row.PaneCreated = strconv.FormatInt(pane.CreatedAt.Unix(), 10)
	return row
}

// sidecarJoinSession builds a minimal named session with one identified pane
// for sidecar join tests.
func sidecarJoinSession(name string) tmux.Session {
	return tmux.Session{
		ID:        "$" + name,
		Name:      name,
		CreatedAt: time.Unix(1752000000, 0),
		Windows: []tmux.Window{{
			ID:      "@1-" + name,
			Active:  true,
			Session: "$" + name,
			Panes: []tmux.Pane{{
				ID:        "%1-" + name,
				Active:    true,
				CreatedAt: time.Unix(1752000000, 0),
			}},
		}},
	}
}

func TestLiveEvidenceOutranksStaleTeardownMark(t *testing.T) {
	t.Parallel()

	// AC1: genuine live/operator evidence beats stale teardown marks. The pane
	// carries both a mark (metadata + sidecar) and unmistakable live-working
	// output; it must classify as running / Active Agents, never Marked For
	// Teardown.
	session := agentSessionForGroup("resumed-worker", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.TeardownMarkedAt = "2026-08-12T10:00:00Z"
	pane.Cockpit.JanitorState = "marked_for_teardown"
	pane.PreviewText = "building plan\nesc to interrupt\n"
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"resumed-worker": sidecarRowFor(session, janitorSessionStatus{JanitorState: "marked_for_teardown", KillNotBefore: "2099-01-01T00:00:00Z"}),
	})
	if got := paneAttentionState(m, session, *pane); got != "running" {
		t.Fatalf("paneAttentionState() = %q, want running (live evidence must outrank stale mark)", got)
	}
	if got := cockpitGroupFor(m, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
	// The stale mark still shows as non-authoritative cleanup metadata.
	if line := cockpitCleanupLine(m, session, *pane, time.Unix(1752000100, 0)); !strings.Contains(line, "marked for teardown") {
		t.Fatalf("stale mark must remain visible as cleanup metadata, got %q", line)
	}
}

func TestOperatorPromptOutranksStaleTeardownMark(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("prompting-worker", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.TeardownMarkedAt = "2026-08-12T10:00:00Z"
	pane.PreviewText = "Apply changes?\n1. approve\n2. reject\n"
	m := modelWithJanitorSidecar(nil)
	if got := paneAttentionState(m, session, *pane); got != "awaiting-operator" {
		t.Fatalf("paneAttentionState() = %q, want awaiting-operator", got)
	}
	if got := cockpitGroupFor(m, session).name; got != groupActiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
	}
}

func TestDeliveredIdleDoesNotOutrankTeardownMark(t *testing.T) {
	t.Parallel()

	// Precedence guard: only live-working/awaiting-operator outrank a mark.
	// A delivered-idle completion screen stays marked-for-teardown.
	session := agentSessionForGroup("finished-worker", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.TeardownMarkedAt = "2026-08-12T10:00:00Z"
	pane.PreviewText = "worked for 5m\n› \n"
	m := modelWithJanitorSidecar(nil)
	if got := paneAttentionState(m, session, *pane); got != "marked-for-teardown" {
		t.Fatalf("paneAttentionState() = %q, want marked-for-teardown", got)
	}
}

func TestSidecarRowPaneIdentityMismatchGrantsNoTeardown(t *testing.T) {
	t.Parallel()

	// AC2: a marked sidecar row whose pane identity does not match the current
	// pane (name reuse / replacement pane) must not attach teardown grouping or
	// a countdown, and the mismatch must be visible on the card.
	session := agentSessionForGroup("reused-name", "done")
	row := janitorSessionStatus{
		JanitorState:  "marked_for_teardown",
		KillNotBefore: "2099-01-01T00:00:00Z",
		PaneID:        "%dead-previous-pane",
		PaneCreated:   "1700000000",
	}
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{"reused-name": row})
	if got := cockpitGroupFor(m, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q (mismatched row must not mark)", got, groupDoneHeld.name)
	}
	line := cockpitCleanupLine(m, session, session.Windows[0].Panes[0], time.Unix(1752000100, 0))
	if !strings.Contains(line, "pane identity mismatch") {
		t.Fatalf("mismatch must be visible on the card, got %q", line)
	}
	if strings.Contains(line, "cleanup in ") {
		t.Fatalf("mismatched row must not render a countdown, got %q", line)
	}
}

func TestSidecarRowWithoutIdentityGrantsNoTeardown(t *testing.T) {
	t.Parallel()

	// An older status payload without pane_id/pane_created must not grant
	// teardown truth by session name alone.
	session := agentSessionForGroup("legacy-payload", "done")
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"legacy-payload": {JanitorState: "marked_for_teardown", KillNotBefore: "2099-01-01T00:00:00Z"},
	})
	if got := cockpitGroupFor(m, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q (identity-less row must not mark)", got, groupDoneHeld.name)
	}
	line := cockpitCleanupLine(m, session, session.Windows[0].Panes[0], time.Unix(1752000100, 0))
	if !strings.Contains(line, "no pane identity") {
		t.Fatalf("identity-less row must be visibly ignored, got %q", line)
	}
}

func TestSidecarRowJoinsOnSessionCreatedIdentity(t *testing.T) {
	t.Parallel()

	// Hygiene populates pane_created from the primary pane's
	// #{session_created}; a row carrying the session creation time for the
	// matching pane_id must join.
	session := sidecarJoinSession("hygiene-created")
	session.Windows[0].Panes[0].CreatedAt = time.Unix(1752000555, 0)
	row := janitorSessionStatus{
		JanitorState: "marked_for_teardown",
		PaneID:       "%1-hygiene-created",
		PaneCreated:  "1752000000", // session_created, not pane_created
	}
	if got := janitorRowJoin(row, session); got != janitorJoinOK {
		t.Fatalf("janitorRowJoin() = %v, want janitorJoinOK for session_created identity", got)
	}
}

func TestSidecarCleanupBlockedRoutesToCleanupBlocked(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("evidence-empty-lane", "done")
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"evidence-empty-lane": sidecarRowFor(session, janitorSessionStatus{JanitorState: "cleanup_blocked", LastAction: "refuse", LastRefusal: "evidence_empty"}),
	})
	if got := cockpitGroupFor(m, session).name; got != groupCleanupBlocked.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupCleanupBlocked.name)
	}
}

func TestSidecarMarkRoutesToMarkedForTeardown(t *testing.T) {
	t.Parallel()

	// The janitor marked the session (sidecar fact) even though tmux metadata
	// has not caught up: sidecar facts route the card.
	session := agentSessionForGroup("marked-by-sidecar", "done")
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"marked-by-sidecar": sidecarRowFor(session, janitorSessionStatus{JanitorState: "marked_for_teardown", KillNotBefore: "2026-07-09T23:59:00Z"}),
	})
	if got := cockpitGroupFor(m, session).name; got != groupInactiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInactiveAgents.name)
	}
}

func TestStaleSidecarNeverInfersCleanupEligibility(t *testing.T) {
	t.Parallel()

	// A stale sidecar is a janitor-health warning, not routing input: the
	// completed session stays plain completed debt.
	session := agentSessionForGroup("stale-sidecar-lane", "done")
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"stale-sidecar-lane": {JanitorState: "cleanup_blocked", LastRefusal: "evidence_empty"},
	})
	m.janitorStatus.State = "stale"
	if got := cockpitGroupFor(m, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
	}
}

func TestHeldBeatsSidecarBlockerForGrouping(t *testing.T) {
	t.Parallel()

	// Precedence: an explicit hold on non-live work outranks a janitor
	// refusal/blocker row.
	session := agentSessionForGroup("held-blocked-lane", "done")
	session.Windows[0].Panes[0].Cockpit.HoldReason = "parent review"
	m := modelWithJanitorSidecar(map[string]janitorSessionStatus{
		"held-blocked-lane": {JanitorState: "protected", LastRefusal: "hold_reason_active"},
	})
	if got := cockpitGroupFor(m, session).name; got != groupHeldAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupHeldAgents.name)
	}
}

func TestAgentIdentityBeatsDashboardAndViewerTheft(t *testing.T) {
	t.Parallel()

	// v0.9.4 stole agents whose Cockpit.Goal or pane PreviewText merely mentioned
	// dashboard/cass-agents/tmuxwatch/localhost/vite. Agent identity now wins.
	thief := "build the dashboard for cass-agents / tmuxwatch on http://localhost:5173 with vite"

	t.Run("goal", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("clean-draft-extract-job", "running")
		session.Windows[0].Panes[0].Cockpit.Goal = thief
		if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
			t.Fatalf("goal theft: cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
		}
	})

	t.Run("preview", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("clean-draft-extract-job", "waiting")
		session.Windows[0].Panes[0].PreviewText = "opening " + thief
		if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
			t.Fatalf("preview theft: cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
		}
	})
}

func TestLiveAgentSubStatesStayInteractive(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"starting", "running", "waiting", "blocked", "review"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			session := agentSessionForGroup("live-agent-"+state, state)
			if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
				t.Fatalf("state %q: cockpitGroupFor() = %q, want %q", state, got, groupActiveAgents.name)
			}
		})
	}

	t.Run("live-held", func(t *testing.T) {
		t.Parallel()
		// A hold blocks cleanup, but live work still belongs in Active.
		session := agentSessionForGroup("held-live-agent", "running")
		session.Windows[0].Panes[0].Cockpit.HoldReason = "awaiting evidence capture"
		if got := cockpitGroupFor(nil, session).name; got != groupActiveAgents.name {
			t.Fatalf("live+held: cockpitGroupFor() = %q, want %q", got, groupActiveAgents.name)
		}
	})

	t.Run("marked-held", func(t *testing.T) {
		t.Parallel()
		// Held+marked is a conflict: the hold wins and the pane must render as
		// blocked, never as a clean countdown (lifecycle contract precedence 4).
		session := agentSessionForGroup("held-marked-agent", "running")
		session.Windows[0].Panes[0].Cockpit.HoldReason = "awaiting evidence capture"
		session.Windows[0].Panes[0].Cockpit.TeardownMarkedAt = "2026-07-08T01:26:00Z"
		session.Windows[0].Panes[0].Cockpit.JanitorState = "marked_for_teardown"
		if got := cockpitGroupFor(nil, session).name; got != groupHeldAgents.name {
			t.Fatalf("marked+held: cockpitGroupFor() = %q, want %q", got, groupHeldAgents.name)
		}
	})
}

func TestTerminalAgentStatesRouteToSystemProblems(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"failed", "route-fail", "safety-fail"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			session := agentSessionForGroup("terminal-agent-"+state, state)
			if got := cockpitGroupFor(nil, session).name; got != groupFailedAgents.name {
				t.Fatalf("state %q: cockpitGroupFor() = %q, want %q", state, got, groupFailedAgents.name)
			}
		})
	}

	t.Run("stale", func(t *testing.T) {
		t.Parallel()
		// Stale is completed-ish debt, not a janitor mark: Completed Agent Runs.
		session := agentSessionForGroup("terminal-agent-stale", "stale")
		if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
			t.Fatalf("stale: cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
		}
	})

	t.Run("dead-nonzero", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("dead-fail-agent", "running")
		session.Windows[0].Panes[0].Dead = true
		session.Windows[0].Panes[0].DeadStatus = 1
		if got := cockpitGroupFor(nil, session).name; got != groupFailedAgents.name {
			t.Fatalf("dead-nonzero: cockpitGroupFor() = %q, want %q", got, groupFailedAgents.name)
		}
	})
}

func TestStaleAgentRoutesCompletedWithAndWithoutPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		preview string
	}{
		{name: "no-preview"},
		{name: "with-preview", preview: "waiting for capture\n› "},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			session := agentSessionForGroup("stale-agent-"+tt.name, "stale")
			session.Windows[0].Panes[0].PreviewText = tt.preview
			if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
				t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
			}
		})
	}
}

func TestServiceWithAgentLabelStaysService(t *testing.T) {
	t.Parallel()

	// A service card can carry an @oc_agent label; it must not be pulled into
	// Active Agents by the managed-agent identity check.
	session := sessionForGroup("smonitor", "service", "/workspace/config/smonitor", "smonitor")
	session.Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{
		ContractVersion: "service-card.v1",
		ManagedBy:       "manual_adopt",
		Kind:            "service",
		Agent:           "service",
		State:           "running",
	}
	if got := cockpitGroupFor(nil, session).name; got != groupServices.name {
		t.Fatalf("service with agent label: cockpitGroupFor() = %q, want %q", got, groupServices.name)
	}
}

func sessionForGroup(name, cmd, path, title string) tmux.Session {
	return tmux.Session{
		ID:   "$" + name,
		Name: name,
		Windows: []tmux.Window{{
			ID:      "@1-" + name,
			Name:    cmd,
			Active:  true,
			Session: "$" + name,
			Panes: []tmux.Pane{{
				ID:          "%1-" + name,
				Active:      true,
				CurrentCmd:  cmd,
				CurrentPath: path,
				Title:       title,
			}},
		}},
	}
}

func withActivity(session tmux.Session, ts time.Time) tmux.Session {
	session.LastActivity = ts
	for wi := range session.Windows {
		session.Windows[wi].LastPane = ts
		for pi := range session.Windows[wi].Panes {
			session.Windows[wi].Panes[pi].LastActivity = ts
		}
	}
	return session
}
