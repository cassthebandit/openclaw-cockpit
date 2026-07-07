package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestCockpitGroupForCurrentFleetShapes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session tmux.Session
		want    string
	}{
		{
			name: "fable session is a live interactive agent",
			session: sessionForGroup("clean-draft-fable-extract", "claude.exe",
				"/Users/cass/projects/clean-draft", "Implement V1 extract job artifacts"),
			want: groupInteractiveAgents.name,
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
		"cass-agents",
		"clean-draft-daniel-brief-html",
		"scratch",
		"camera-rtsp",
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
	if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupSystemProblems.name)
	}
}

func TestCockpitGroupTreatsHoldReasonAsAnnotation(t *testing.T) {
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
	if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
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
	if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupSystemProblems.name)
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
	// Interactive Agents band per the committee taxonomy (§9.2).
	if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
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
	if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
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
	if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
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
	if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupSystemProblems.name)
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
	// state falls back to Your Call.
	if got := cockpitGroupFor(m, session).name; got != groupYourCall.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupYourCall.name)
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

	if got := cockpitGroupFor(nil, session).name; got != groupRuntime.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupRuntime.name)
	}
}

func TestOpenClawRuntimePresentationGroupRoutesRuntimeCards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		presentationGroup string
		want              string
	}{
		{name: "current work", presentationGroup: "current_work", want: groupRuntime.name},
		{name: "needs decision", presentationGroup: "needs_decision", want: groupYourCall.name},
		{name: "route health", presentationGroup: "route_health", want: groupRuntime.name},
		{name: "delivery handoff", presentationGroup: "delivery_handoff", want: groupRuntime.name},
		{name: "source unknown", presentationGroup: "source_unknown", want: groupRuntime.name},
		{name: "expected controls", presentationGroup: "expected_controls", want: groupExpectedControls.name},
		{name: "skeletons", presentationGroup: "skeletons", want: groupSkeletons.name},
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

	runtimeCols, runtimeInner := m.cardLayoutForGroup(groupRuntime, 10)
	if runtimeCols != 5 {
		t.Fatalf("runtime cols = %d, want 5 despite live --cols cap", runtimeCols)
	}
	if runtimeInner < 30 {
		t.Fatalf("runtime inner width = %d, want readable 5-across width", runtimeInner)
	}

	decisionCols, _ := m.cardLayoutForGroup(groupYourCall, 10)
	if decisionCols != 3 {
		t.Fatalf("Your Call cols = %d, want spacious 3-column cap", decisionCols)
	}
}

func TestRuntimeGroupLayoutRejectsCrampedFiveAcross(t *testing.T) {
	t.Parallel()

	m := NewModel(nil, time.Second, 4, nil, false, true)
	m.SetOrganized(true)
	m.width = 150
	m.height = 50
	m.preferredCols = 4

	cols, inner := m.cardLayoutForGroup(groupRuntime, 10)
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

	cols, _ := m.cardLayoutForGroup(groupRuntime, len(m.sessions))
	if cols != 5 {
		t.Fatalf("runtime render cols = %d, want 5", cols)
	}
	rows := (len(m.sessions) + cols - 1) / cols
	if rows != 2 {
		t.Fatalf("runtime rows = %d, want 2 rows for 10 cards at 5-across", rows)
	}
	heights := m.cardBodyHeightsByGroup(m.sessions)
	if heights[groupRuntime.name] <= 0 {
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
	m.sessions[4].Windows[0].Panes[0].Cockpit = &tmux.CockpitMeta{Kind: "agent", Agent: "claude", State: "done"}

	heights := m.cardBodyHeightsByGroup(m.sessions)
	running := heights[groupInteractiveAgents.name]
	services := heights[groupServices.name]
	done := heights[groupDoneHeld.name]

	if running <= maxOverviewBodyLines {
		t.Fatalf("interactive agent height = %d, want more than old fixed cap %d", running, maxOverviewBodyLines)
	}
	if services <= 0 || services > 8 {
		t.Fatalf("service height = %d, want compact service budget", services)
	}
	if done <= 0 || done > 7 {
		t.Fatalf("done height = %d, want compact completed budget", done)
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
		{state: "waiting", want: groupInteractiveAgents.name},
		{state: "blocked", want: groupInteractiveAgents.name},
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
	for _, want := range []string{groupInteractiveAgents.name, groupServices.name} {
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

func TestAgentIdentityBeatsDashboardAndViewerTheft(t *testing.T) {
	t.Parallel()

	// v0.9.4 stole agents whose Cockpit.Goal or pane PreviewText merely mentioned
	// dashboard/cass-agents/tmuxwatch/localhost/vite. Agent identity now wins.
	thief := "build the dashboard for cass-agents / tmuxwatch on http://localhost:5173 with vite"

	t.Run("goal", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("clean-draft-extract-job", "running")
		session.Windows[0].Panes[0].Cockpit.Goal = thief
		if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
			t.Fatalf("goal theft: cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
		}
	})

	t.Run("preview", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("clean-draft-extract-job", "waiting")
		session.Windows[0].Panes[0].PreviewText = "opening " + thief
		if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
			t.Fatalf("preview theft: cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
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
			if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
				t.Fatalf("state %q: cockpitGroupFor() = %q, want %q", state, got, groupInteractiveAgents.name)
			}
		})
	}

	t.Run("live-held", func(t *testing.T) {
		t.Parallel()
		// A hold means "don't reap", not "finished": a live held agent stays live.
		session := agentSessionForGroup("held-live-agent", "running")
		session.Windows[0].Panes[0].Cockpit.HoldReason = "awaiting evidence capture"
		if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
			t.Fatalf("live+held: cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
		}
	})
}

func TestTerminalAgentStatesRouteToSystemProblems(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"failed", "route-fail", "safety-fail", "stale"} {
		state := state
		t.Run(state, func(t *testing.T) {
			t.Parallel()
			session := agentSessionForGroup("terminal-agent-"+state, state)
			if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
				t.Fatalf("state %q: cockpitGroupFor() = %q, want %q", state, got, groupSystemProblems.name)
			}
		})
	}

	t.Run("dead-nonzero", func(t *testing.T) {
		t.Parallel()
		session := agentSessionForGroup("dead-fail-agent", "running")
		session.Windows[0].Panes[0].Dead = true
		session.Windows[0].Panes[0].DeadStatus = 1
		if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
			t.Fatalf("dead-nonzero: cockpitGroupFor() = %q, want %q", got, groupSystemProblems.name)
		}
	})
}

func TestServiceWithAgentLabelStaysService(t *testing.T) {
	t.Parallel()

	// A service card can carry an @oc_agent label; it must not be pulled into
	// Interactive Agents by the managed-agent identity check.
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
