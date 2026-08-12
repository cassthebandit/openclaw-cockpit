package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// releaseTagScript resolves the checkout script relative to the repo root.
func releaseTagScript(t *testing.T) string {
	t.Helper()
	top := gitOutput(t, "", "rev-parse", "--show-toplevel")
	script := filepath.Join(top, ".github", "scripts", "checkout-release-tag.sh")
	if _, err := os.Stat(script); err != nil {
		t.Fatalf("release tag script missing: %v", err)
	}
	return script
}

// releaseFixtureRepo builds a throwaway git repo with one commit tagged
// v1.2.3 so the checkout script can be evaluated for real.
func releaseFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "--quiet")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("release fixture\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	git("add", "-A")
	git("-c", "user.email=test@invalid", "-c", "user.name=release-test", "commit", "--quiet", "-m", "fixture")
	git("tag", "v1.2.3")
	return dir
}

func runReleaseScript(t *testing.T, repo, tag string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command("bash", releaseTagScript(t))
	cmd.Dir = repo
	cmd.Env = append(os.Environ(), "RELEASE_TAG="+tag)
	return cmd.CombinedOutput()
}

// TestReleaseTagScriptRejectsMaliciousAndMissingTags proves AC12's tag half
// by evaluating the actual workflow script: shell metacharacters can never
// become syntax (the sentinel file is not created), and only an exact
// existing vX.Y.Z tag is accepted.
func TestReleaseTagScriptRejectsMaliciousAndMissingTags(t *testing.T) {
	repo := releaseFixtureRepo(t)
	sentinel := filepath.Join(t.TempDir(), "pwned")

	malicious := []string{
		"v1.2.3; touch " + sentinel,
		"$(touch " + sentinel + ")",
		"`touch " + sentinel + "`",
		"v1.2.3 && touch " + sentinel,
		"",
		"main",
		"v1.2",
		"v1.2.3-rc1",
	}
	for _, tag := range malicious {
		out, err := runReleaseScript(t, repo, tag)
		if err == nil {
			t.Fatalf("tag %q must be rejected, output: %s", tag, out)
		}
		if _, statErr := os.Stat(sentinel); statErr == nil {
			t.Fatalf("tag %q executed injected shell syntax", tag)
		}
	}

	// A well-formed but nonexistent tag is rejected as missing.
	if out, err := runReleaseScript(t, repo, "v9.9.9"); err == nil {
		t.Fatalf("nonexistent tag must be rejected, output: %s", out)
	} else if !strings.Contains(string(out), "does not exist") {
		t.Fatalf("nonexistent tag error should say so, got: %s", out)
	}

	// The exact existing tag checks out detached at the tagged commit.
	if out, err := runReleaseScript(t, repo, "v1.2.3"); err != nil {
		t.Fatalf("valid tag rejected: %v\n%s", err, out)
	}
	head := gitOutput(t, repo, "rev-parse", "HEAD")
	tagCommit := gitOutput(t, repo, "rev-parse", "v1.2.3^{commit}")
	if head != tagCommit {
		t.Fatalf("checkout landed on %s, want tagged commit %s", head, tagCommit)
	}
}
