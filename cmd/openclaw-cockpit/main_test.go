package main

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testBinPath string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "openclaw-cockpit-test-")
	if err != nil {
		log.Fatalf("failed to create temp dir for test binary: %v", err)
	}
	testBinPath = filepath.Join(tmpDir, "openclaw-cockpit-test")

	buildCmd := exec.Command("go", "build", "-o", testBinPath, ".")
	if output, err := buildCmd.CombinedOutput(); err != nil {
		log.Fatalf("failed to build binary: %v\nOutput: %s", err, string(output))
	}

	code := m.Run()
	if err := os.RemoveAll(tmpDir); err != nil {
		log.Printf("failed to remove test temp dir %s: %v", tmpDir, err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

// gitOutput runs git with the given args, skipping the test when git or the
// repository is unavailable (for example a source tarball build).
func gitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("git %v unavailable: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

// TestVersionFlag verifies --version reports product and version and states
// its identity explicitly (either a revision or "revision unknown"), never a
// bare unqualified banner.
func TestVersionFlag(t *testing.T) {
	cmd := exec.Command(testBinPath, "--version")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("--version flag failed: %v, output: %s", err, output)
	}

	outputStr := strings.TrimSpace(string(output))
	prefix := productName + " " + version
	if !strings.HasPrefix(outputStr, prefix) {
		t.Fatalf("expected version output to start with %q, got %q", prefix, outputStr)
	}
	if !strings.Contains(outputStr, "rev ") && !strings.Contains(outputStr, "revision unknown") {
		t.Fatalf("--version must state revision identity explicitly, got %q", outputStr)
	}
}

// identityCloneDir copies the current working tree (including uncommitted
// repair work) into a fresh temp git repository with a real .git directory
// and commits it. This toolchain silently skips VCS stamping when .git is a
// gitdir file (linked worktree), so identity proofs must build from a normal
// repository — the same mechanism the installer uses; committing the copy
// gives the build a known clean revision to assert against.
func identityCloneDir(t *testing.T) (dir, head string) {
	t.Helper()
	top := gitOutput(t, "", "rev-parse", "--show-toplevel")
	dir = filepath.Join(t.TempDir(), "repo")
	if out, err := exec.Command("cp", "-R", top, dir).CombinedOutput(); err != nil {
		t.Fatalf("copy working tree: %v\n%s", err, out)
	}
	if err := os.RemoveAll(filepath.Join(dir, ".git")); err != nil {
		t.Fatalf("drop copied gitdir link: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet")
	git("add", "-A")
	git("-c", "user.email=test@invalid", "-c", "user.name=identity-test", "commit", "--quiet", "-m", "identity fixture")
	head = gitOutput(t, dir, "rev-parse", "HEAD")
	return dir, head
}

func buildIdentityBinary(t *testing.T, cloneDir string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "cockpit-identity-test")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/openclaw-cockpit")
	cmd.Dir = cloneDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build in clone: %v\n%s", err, out)
	}
	return bin
}

func runBuildInfo(t *testing.T, bin string) buildIdentity {
	t.Helper()
	out, err := exec.Command(bin, "--build-info").Output()
	if err != nil {
		t.Fatalf("--build-info failed: %v", err)
	}
	var identity buildIdentity
	if err := json.Unmarshal(out, &identity); err != nil {
		t.Fatalf("--build-info is not valid JSON: %v\n%s", err, out)
	}
	return identity
}

// TestBuildInfoIdentityIsTruthful verifies AC9 end to end on a real build:
// a clean committed build reports the exact revision with modified=false,
// and an uncommitted edit flips the identity to dirty.
func TestBuildInfoIdentityIsTruthful(t *testing.T) {
	cloneDir, head := identityCloneDir(t)

	cleanBin := buildIdentityBinary(t, cloneDir)
	identity := runBuildInfo(t, cleanBin)
	if identity.Product != productName || identity.Version != version {
		t.Fatalf("identity product/version = %q/%q, want %q/%q", identity.Product, identity.Version, productName, version)
	}
	if identity.IdentitySource != "go-build-metadata" {
		t.Fatalf("identity source = %q, want go-build-metadata", identity.IdentitySource)
	}
	if identity.VCSRevision != head {
		t.Fatalf("identity revision = %q, want %q", identity.VCSRevision, head)
	}
	if identity.VCSModified == nil || *identity.VCSModified {
		t.Fatalf("clean committed build must report modified=false, got %+v", identity.VCSModified)
	}

	// --version carries the same truth in human form.
	versionOut, err := exec.Command(cleanBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version on clean build: %v", err)
	}
	if !strings.Contains(string(versionOut), "rev "+head[:12]) || !strings.Contains(string(versionOut), "clean") {
		t.Fatalf("clean --version = %q, want rev %s + clean", strings.TrimSpace(string(versionOut)), head[:12])
	}

	// Dirty the clone and rebuild: identity must flip to dirty.
	mainPath := filepath.Join(cloneDir, "cmd", "openclaw-cockpit", "main.go")
	source, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if err := os.WriteFile(mainPath, append(source, []byte("\n// dirty-marker\n")...), 0o644); err != nil {
		t.Fatalf("dirty main.go: %v", err)
	}
	dirtyBin := buildIdentityBinary(t, cloneDir)
	dirtyIdentity := runBuildInfo(t, dirtyBin)
	if dirtyIdentity.VCSModified == nil || !*dirtyIdentity.VCSModified {
		t.Fatalf("uncommitted edit must report modified=true, got %+v", dirtyIdentity.VCSModified)
	}
	dirtyVersion, err := exec.Command(dirtyBin, "--version").CombinedOutput()
	if err != nil {
		t.Fatalf("--version on dirty build: %v", err)
	}
	if !strings.Contains(string(dirtyVersion), "dirty") {
		t.Fatalf("dirty --version = %q, want dirty marker", strings.TrimSpace(string(dirtyVersion)))
	}
}

// TestBuildIdentityHumanRendering pins the human formats, including the
// explicit unknown case (never inferring clean parity from a missing fact).
func TestBuildIdentityHumanRendering(t *testing.T) {
	unknown := buildIdentity{Product: "OpenClaw Cockpit", Version: "1.0.0"}
	if got := unknown.human(); !strings.Contains(got, "revision unknown") {
		t.Fatalf("identity without VCS evidence must say revision unknown, got %q", got)
	}
	clean := false
	known := buildIdentity{Product: "OpenClaw Cockpit", Version: "1.0.0", VCSRevision: "abcdef0123456789", VCSModified: &clean}
	if got := known.human(); !strings.Contains(got, "rev abcdef012345") || !strings.Contains(got, "clean") {
		t.Fatalf("identity rendering wrong: %q", got)
	}
	dirty := true
	known.VCSModified = &dirty
	if got := known.human(); !strings.Contains(got, "dirty") {
		t.Fatalf("dirty identity must say dirty: %q", got)
	}
}

// TestInvalidDebugClick verifies invalid --debug-click values are rejected
func TestInvalidDebugClick(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		expectError string
	}{
		{
			name:        "single value",
			value:       "10",
			expectError: "invalid --debug-click value",
		},
		{
			name:        "three values",
			value:       "10,20,30",
			expectError: "invalid --debug-click value",
		},
		{
			name:        "invalid x coordinate",
			value:       "abc,20",
			expectError: "invalid debug-click x coordinate",
		},
		{
			name:        "invalid y coordinate",
			value:       "10,xyz",
			expectError: "invalid debug-click y coordinate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command(testBinPath, "--debug-click", tt.value)
			output, err := cmd.CombinedOutput()

			if err == nil {
				t.Errorf("expected error for --debug-click=%q, got none", tt.value)
			}

			outputStr := string(output)
			if !strings.Contains(outputStr, tt.expectError) {
				t.Errorf("expected error containing %q, got: %s", tt.expectError, outputStr)
			}
		})
	}
}

// TestInvalidIntervalFlag verifies invalid --interval values are rejected
func TestInvalidIntervalFlag(t *testing.T) {
	cmd := exec.Command(testBinPath, "--interval", "invalid")
	output, err := cmd.CombinedOutput()

	if err == nil {
		t.Error("expected error for invalid --interval value")
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "invalid value") {
		t.Errorf("expected error message about invalid value, got: %s", outputStr)
	}
}

// TestHelpFlag verifies -h/--help flags work
func TestHelpFlag(t *testing.T) {
	cmd := exec.Command(testBinPath, "-h")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-h flag failed: %v, output: %s", err, output)
	}

	outputStr := string(output)
	if !strings.Contains(outputStr, "Usage of") && !strings.Contains(outputStr, "interval") {
		t.Errorf("expected help output to contain usage information, got: %s", outputStr)
	}
	if strings.Contains(outputStr, "session kills") {
		t.Errorf("--control help should not advertise session kills, got: %s", outputStr)
	}
}

// TestOpenClawRuntimeIntervalDefault pins the runtime-card refresh cadence at
// five seconds and keeps it independent of the one-second tmux poll interval.
// Both defaults are read back out of the built binary's own flag help so the
// test fails if either constant drifts.
func TestOpenClawRuntimeIntervalDefault(t *testing.T) {
	if defaultOpenClawRuntimeInterval != 5*time.Second {
		t.Fatalf("defaultOpenClawRuntimeInterval = %s, want 5s", defaultOpenClawRuntimeInterval)
	}

	cmd := exec.Command(testBinPath, "-h")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("-h flag failed: %v, output: %s", err, output)
	}
	help := string(output)

	if !strings.Contains(help, "-openclaw-runtime-interval duration") {
		t.Fatalf("help does not document -openclaw-runtime-interval, got: %s", help)
	}
	runtimeDefault := flagDefaultFromHelp(t, help, "openclaw-runtime-interval")
	if runtimeDefault != "5s" {
		t.Errorf("-openclaw-runtime-interval default = %q, want %q", runtimeDefault, "5s")
	}
	tmuxDefault := flagDefaultFromHelp(t, help, "interval")
	if tmuxDefault != "1s" {
		t.Errorf("-interval default = %q, want %q (tmux sampling must stay independent)", tmuxDefault, "1s")
	}
}

// flagDefaultFromHelp extracts the "(default X)" value flag prints for the
// named flag.
func flagDefaultFromHelp(t *testing.T, help, name string) string {
	t.Helper()
	lines := strings.Split(help, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) != "-"+name+" duration" {
			continue
		}
		for _, following := range lines[i+1:] {
			if strings.HasPrefix(strings.TrimSpace(following), "-") {
				break
			}
			_, after, found := strings.Cut(following, "(default ")
			if !found {
				continue
			}
			value, _, _ := strings.Cut(after, ")")
			return strings.TrimSpace(value)
		}
		t.Fatalf("no default reported for -%s in help: %s", name, help)
	}
	t.Fatalf("flag -%s not found in help: %s", name, help)
	return ""
}

func TestParseSessionList(t *testing.T) {
	got := parseSessionList(" cass-agents, watch ,,,services ")
	want := []string{"cass-agents", "watch", "services"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("parseSessionList() = %#v, want %#v", got, want)
	}
}

func TestEffectiveMonitorOnlyRequiresExplicitControl(t *testing.T) {
	tests := []struct {
		name        string
		monitorFlag bool
		control     bool
		want        bool
	}{
		{name: "default monitor only", monitorFlag: true, control: false, want: true},
		{name: "legacy false flag does not grant control", monitorFlag: false, control: false, want: true},
		{name: "control opts into authority", monitorFlag: true, control: true, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveMonitorOnly(tt.monitorFlag, tt.control); got != tt.want {
				t.Fatalf("effectiveMonitorOnly() = %v, want %v", got, tt.want)
			}
		})
	}
}
