// Package testutil owns disposable integration-test resources. Production
// packages must not depend on it.
package testutil

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TmuxWrapper returns a tmux wrapper pinned to a unique, config-free server.
// The socket lives in short, test-owned temp space even when TMPDIR or the
// test's name exceeds macOS's Unix socket path limit. Cleanup kills only this
// server and removes its socket directory, including an abandoned socket file.
func TmuxWrapper(t *testing.T) string {
	t.Helper()
	real, err := exec.LookPath("tmux")
	if err != nil {
		t.Skip("tmux not installed; disposable-server test skipped")
	}
	directory, err := os.MkdirTemp("/tmp", "oc-")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(directory, "s")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, real, "-f", "/dev/null", "-S", socket, "kill-server").CombinedOutput()
		if err != nil && !strings.Contains(string(out), "No such file or directory") && !strings.Contains(string(out), "no server running") {
			t.Errorf("kill disposable tmux server %s: %v: %s", socket, err, out)
		}
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove disposable tmux directory: %v", err)
		}
	})
	wrapper := filepath.Join(t.TempDir(), "tmux")
	quote := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
	script := "#!/bin/sh\nexec " + quote(real) + " -f /dev/null -S " + quote(socket) + " \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatalf("write tmux wrapper: %v", err)
	}
	// A freshly written executable can transiently yield ETXTBSY on Linux.
	// Retry only this read-only readiness probe, never a server mutation.
	deadline := time.Now().Add(time.Second)
	for {
		err := exec.Command(wrapper, "-V").Run()
		if err == nil {
			return wrapper
		}
		if !errors.Is(err, syscall.ETXTBSY) || time.Now().After(deadline) {
			t.Fatalf("probe tmux fixture: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TmuxOut runs exactly one fixture command without retries.
func TmuxOut(t *testing.T, wrapper string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, wrapper, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v\n%s", args, err, out)
	}
	return string(out)
}
