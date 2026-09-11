package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/cassthebandit/openclaw-cockpit/internal/tmux"
)

func TestRegressionCappedStderrConsumesWholeWrite(t *testing.T) {
	b := &cappedBuffer{limit: 4}
	n, err := b.Write([]byte("abcdefgh"))
	if n != 8 || err != nil {
		t.Fatalf("n=%d err=%v stored=%q; want n=8 with 4 retained", n, err, b.String())
	}
}

func TestRegressionRuntimeTimeoutIncludesDescendantPipes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "producer.py")
	script := "import subprocess,sys\nsubprocess.Popen([sys.executable,'-c','import time; time.sleep(1.2)'])\nprint('{\"cardContract\":\"runtime-card.v1\",\"cards\":[]}')\n"
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err := loadOpenClawRuntimeCards(RuntimeSource{Enabled: true, Script: path, Timeout: 100 * time.Millisecond})
	elapsed := time.Since(start)
	if elapsed > 600*time.Millisecond {
		t.Fatalf("100ms timeout took %s (err=%v)", elapsed, err)
	}
}

func TestRegressionRuntimeRefreshSurvivesTmuxFailure(t *testing.T) {
	client, err := tmux.NewClient("/bin/echo")
	if err != nil {
		t.Fatal(err)
	}
	m := NewModel(client, time.Second, 1, nil, false, true)
	m.runtime.Enabled = true
	m.Update(errMsg{err: errors.New("tmux unavailable")})
	card := tmux.Session{ID: "runtime-new", Name: "new runtime job"}
	m.Update(runtimeCardsMsg{sessions: []tmux.Session{card}, loadedAt: time.Now()})
	if !m.sessionExists(card.ID) {
		t.Fatalf("runtime cache=%d, displayed sessions=%d; refresh still requires a successful tmux snapshot", len(m.runtimeSessions), len(m.sessions))
	}
}

func TestRegressionDistinctViewerHeadersStayDistinct(t *testing.T) {
	now := time.Now()
	p := tmux.Pane{Cockpit: &tmux.CockpitMeta{ContractVersion: "display-only", ManagedBy: "manual_adopt", Kind: "viewer", Owner: "operator", Project: "study", StartedAt: now.Add(-time.Hour).Format(time.RFC3339)}}
	a := formatHeader(now, 38, tmux.Session{Name: "red-design"}, tmux.Window{}, p, false, false, false, false, "", "[^] [-] [x]", "")
	b := formatHeader(now, 38, tmux.Session{Name: "blue-design"}, tmux.Window{}, p, false, false, false, false, "", "[^] [-] [x]", "")
	if a == b {
		t.Fatalf("different sessions have identical header: %q", a)
	}
}

func TestRegressionConfigRejectsTrailingGarbage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{} {"not_a_setting":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadWallConfig(path)
	if err == nil {
		t.Fatal("accepted two JSON documents despite strict config contract")
	}
}

func TestRegressionLaunchAgeRemainsLaunchAgeAfterCompletion(t *testing.T) {
	now := time.Now()
	p := tmux.Pane{Cockpit: &tmux.CockpitMeta{StartedAt: now.Add(-time.Hour).Format(time.RFC3339), CompletedAt: now.Add(-time.Minute).Format(time.RFC3339)}}
	s := tmux.Session{Name: "worker", CreatedAt: now.Add(-48 * time.Hour)}
	got := formatHeader(now, 100, s, tmux.Window{}, p, false, false, false, false, "", "", "")
	if !strings.Contains(got, "launched 1h") {
		t.Fatalf("known launch age discarded after completion: %q", got)
	}
}

func TestRegressionLayoutFitsCommonTerminalSizes(t *testing.T) {
	for _, size := range [][2]int{{40, 12}, {80, 24}, {100, 30}, {158, 43}, {220, 60}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			client, err := tmux.NewClient("/bin/echo")
			if err != nil {
				t.Fatal(err)
			}
			m := NewModel(client, time.Second, 8, nil, false, true)
			m.SetOrganized(true)
			for i := 0; i < 12; i++ {
				m.sessions = append(m.sessions, captureTestSession(fmt.Sprintf("$%d", i), fmt.Sprintf("%%%d", i), tmux.CockpitMeta{ManagedBy: "agent_wall", Kind: "agent", Agent: "codex", State: "running", Goal: strings.Repeat("job ", 30)}))
			}
			m.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			got := m.View().Content
			if lipgloss.Width(got) > size[0] || lipgloss.Height(got) > size[1] {
				t.Fatalf("viewport %v rendered %dx%d", size, lipgloss.Width(got), lipgloss.Height(got))
			}
		})
	}
}

func TestRegressionEvidenceSymlinkCannotEscapeRunRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "run")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "other-run.json")
	if err := os.WriteFile(outside, []byte(`{"status":"PASS"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "result.json")); err != nil {
		t.Fatal(err)
	}
	pane := tmux.Pane{Cockpit: &tmux.CockpitMeta{RunRoot: root, EvidencePath: "result.json"}}
	if got := artifactOutcomeState(pane); got != "review" {
		t.Fatalf("out-of-root symlink classified %q instead of review", got)
	}
}
