// File lifecycle_test.go exercises the runtime-aware, read-only idle-finished
// detector across Codex, Fable/Claude, Gemini, and AGY pane fixtures.
package ui

import (
	"strings"
	"testing"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

// Captured-style pane tails. Each positive ends at an idle prompt after a
// completion/summary line. The non-Codex positives deliberately avoid
// "openai codex", "gpt-", and "worked for" (committee §9.3 / §9.6).
const (
	codexIdleFinished = `● Ran the extract job and updated the changelog.
  - internal/extract/job.go
  - CHANGELOG.md

  Worked for 3m 12s · 2 files changed

› `

	claudeIdleFinished = `● Finished the v1 extract artifacts and wired the tests.

  Cooked for 4m 08s · 6 tool uses

╭──────────────────────────────────────────────╮
│ › Try "review the diff"                        │
╰──────────────────────────────────────────────╯`

	fableIdleFinished = `● Landed the guardrails and reran the suite — all green.

  Brewed for 1m 52s

│ › `

	geminiIdleFinished = `✦ Completed the refactor across 3 files and verified the build.

  Completed in 1m 45s

> Type your message or @path/to/file`

	agyIdleFinished = `Antigravity wrapped up the migration.

  Task complete — ran for 52s

❯ `

	// Active negatives: a completion + prompt is present but an active marker
	// vetoes, or a required marker is missing.
	codexStillWorking = `● Working through the extract job…

  Worked for 1m 02s
  Esc to interrupt

› `

	claudeBackgroundRunning = `● Kicked off the build in the background.

  waiting for background terminal

│ › `

	runningNoCompletion = `● Thinking about the plan and reading files.

› `

	completionNoPrompt = `● All done — everything landed.

  Worked for 2m 00s`
)

func TestScreenIsIdleFinishedPositives(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"codex":  codexIdleFinished,
		"claude": claudeIdleFinished,
		"fable":  fableIdleFinished,
		"gemini": geminiIdleFinished,
		"agy":    agyIdleFinished,
	}
	for name, fixture := range cases {
		name, fixture := name, fixture
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if !screenIsIdleFinished(fixture) {
				t.Fatalf("%s fixture should be idle-finished:\n%s", name, fixture)
			}
		})
	}
}

func TestScreenIsIdleFinishedNonCodexPositivesAvoidCodexTokens(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{"claude": claudeIdleFinished, "fable": fableIdleFinished, "gemini": geminiIdleFinished, "agy": agyIdleFinished} {
		lowered := strings.ToLower(fixture)
		for _, banned := range []string{"openai codex", "gpt-", "worked for"} {
			if strings.Contains(lowered, banned) {
				t.Fatalf("%s positive fixture must not rely on Codex-only token %q", name, banned)
			}
		}
	}
}

func TestScreenIsIdleFinishedNegatives(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"esc to interrupt vetoes":     codexStillWorking,
		"background terminal vetoes":  claudeBackgroundRunning,
		"running without completion":  runningNoCompletion,
		"completion without a prompt": completionNoPrompt,
		"empty screen":                "",
	}
	for name, fixture := range cases {
		name, fixture := name, fixture
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if screenIsIdleFinished(fixture) {
				t.Fatalf("%s should NOT be idle-finished:\n%s", name, fixture)
			}
		})
	}
}

func TestScreenIsIdleFinishedStripsANSI(t *testing.T) {
	t.Parallel()

	ansi := "\x1b[1m● Done.\x1b[0m\n\n  \x1b[32mWorked for 2m 3s\x1b[0m\n\n\x1b[36m›\x1b[0m "
	if !screenIsIdleFinished(ansi) {
		t.Fatalf("ANSI-wrapped completion should be idle-finished after stripping: %q", ansi)
	}
}

func TestPaneIdleFinishedRequiresManagedAgentGate(t *testing.T) {
	t.Parallel()

	// Same finished screen; only the managed-agent gate differs.
	managed := tmux.Pane{
		PreviewText: codexIdleFinished,
		Cockpit: &tmux.CockpitMeta{
			ManagedBy: "agent_wall",
			Kind:      "visible-agent",
			Agent:     "codex",
			State:     "running",
		},
	}
	if !paneIdleFinished(managed) {
		t.Fatalf("managed agent pane with a finished screen should be idle-finished")
	}

	adopted := managed
	adopted.Cockpit = &tmux.CockpitMeta{
		ContractVersion: "display-only",
		ManagedBy:       "manual_adopt",
		Kind:            "agent",
		Agent:           "codex",
		State:           "running",
	}
	if paneIdleFinished(adopted) {
		t.Fatalf("display-only adopted pane must not be idle-finished (gate)")
	}

	service := managed
	service.Cockpit = &tmux.CockpitMeta{
		ManagedBy: "agent_wall",
		Kind:      "service",
		Agent:     "service",
		State:     "running",
	}
	if paneIdleFinished(service) {
		t.Fatalf("service pane must not be idle-finished (gate)")
	}

	plain := tmux.Pane{PreviewText: codexIdleFinished}
	if paneIdleFinished(plain) {
		t.Fatalf("plain pane with no cockpit metadata must not be idle-finished (gate)")
	}
}

func TestIdleFinishedAgentRoutesToCompleted(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("clean-draft-fable-extract", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.Kind = "visible-agent"
	pane.Cockpit.Agent = "fable"
	pane.PreviewText = fableIdleFinished

	if got := sessionAttentionState(nil, session); got != "delivered-idle" {
		t.Fatalf("sessionAttentionState() = %q, want delivered-idle", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
	}
}

func TestLiveAgentWithoutFinishedScreenStaysInteractive(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("clean-draft-fable-extract", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.Kind = "visible-agent"
	pane.Cockpit.Agent = "fable"
	pane.PreviewText = codexStillWorking // completion present but "esc to interrupt" vetoes

	if got := sessionAttentionState(nil, session); got != "running" {
		t.Fatalf("sessionAttentionState() = %q, want running (active marker vetoes idle-finished)", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupInteractiveAgents.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupInteractiveAgents.name)
	}
}

func TestLifecycleOperatorPromptBeatsCompletion(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("fable-ready-to-code", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Cockpit.Kind = "visible-agent"
	pane.Cockpit.Agent = "fable"
	pane.PreviewText = fableIdleFinished + "\nReady to code?\nWould you like to proceed?\n"

	if got := sessionAttentionState(nil, session); got != "awaiting-operator" {
		t.Fatalf("sessionAttentionState() = %q, want awaiting-operator", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupYourCall.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupYourCall.name)
	}
}

func TestLifecycleAvoidsLoosePlanAndFailedMarkers(t *testing.T) {
	t.Parallel()

	for name, text := range map[string]string{
		"planning":  "planning the refactor\n› ",
		"explained": "as explained above\n› ",
		"tests":     "12 passed, 0 failed\n› ",
	} {
		name, text := name, text
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			session := agentSessionForGroup("loose-marker-"+name, "running")
			session.Windows[0].Panes[0].PreviewText = text
			if got := sessionAttentionState(nil, session); got != "running" {
				t.Fatalf("sessionAttentionState() = %q, want running", got)
			}
		})
	}
}

func TestLifecyclePermissionDeniedIsFailure(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("permission-denied", "running")
	session.Windows[0].Panes[0].PreviewText = "error: permission denied\n› "

	if got := sessionAttentionState(nil, session); got != "failed" {
		t.Fatalf("sessionAttentionState() = %q, want failed", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupSystemProblems.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupSystemProblems.name)
	}
}

func TestDeadHeldAgentRoutesToCompleted(t *testing.T) {
	t.Parallel()

	session := agentSessionForGroup("held-dead-agent", "running")
	pane := &session.Windows[0].Panes[0]
	pane.Dead = true
	pane.Cockpit.HoldReason = "evidence hold pending synthesis"

	if got := sessionAttentionState(nil, session); got != "held" {
		t.Fatalf("sessionAttentionState() = %q, want held", got)
	}
	if got := cockpitGroupFor(nil, session).name; got != groupDoneHeld.name {
		t.Fatalf("cockpitGroupFor() = %q, want %q", got, groupDoneHeld.name)
	}
}
