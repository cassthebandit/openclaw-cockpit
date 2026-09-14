package testutil

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTmuxOwnershipAndCleanup(t *testing.T) {
	sentinel := TmuxWrapper(t)
	TmuxOut(t, sentinel, "new-session", "-d", "-s", "sentinel", "sleep 60")
	sentinelSocket := strings.TrimSpace(TmuxOut(t, sentinel, "display-message", "-p", "#{socket_path}"))
	var socket string
	var pid int
	t.Run("owned", func(t *testing.T) {
		wrapper := TmuxWrapper(t)
		TmuxOut(t, wrapper, "new-session", "-d", "-s", "target", "sleep 60")
		socket = strings.TrimSpace(TmuxOut(t, wrapper, "display-message", "-p", "#{socket_path}"))
		if socket == sentinelSocket || len(socket) >= 100 {
			t.Fatalf("socket must be distinct and short: %q versus %q", socket, sentinelSocket)
		}
		var err error
		pid, err = strconv.Atoi(strings.TrimSpace(TmuxOut(t, wrapper, "display-message", "-p", "#{pid}")))
		if err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(wrapper); err != nil || !strings.Contains(string(data), " -f /dev/null -S ") {
			t.Fatalf("wrapper must bypass operator config: %q, %v", data, err)
		}
	})
	if socket == "" || pid <= 0 {
		t.Fatal("owned server was not observed")
	}
	if _, err := os.Stat(filepath.Dir(socket)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("socket directory survived test cleanup: %v", err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("owned server process %d survived cleanup", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
	TmuxOut(t, sentinel, "has-session", "-t", "=sentinel")
}
