//go:build !windows

package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRuntimeDescendantDoesNotSurviveTimeout(t *testing.T) {
	dir := t.TempDir()
	pidfile := filepath.Join(dir, "child.pid")
	script := filepath.Join(dir, "producer.py")
	source := fmt.Sprintf("import subprocess,sys,pathlib,time\np=subprocess.Popen([sys.executable,'-c','import time;time.sleep(60)'])\npathlib.Path(%q).write_text(str(p.pid))\nprint('{}',flush=True)\ntime.sleep(60)\n", pidfile)
	if err := os.WriteFile(script, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := loadOpenClawRuntimeCards(RuntimeSource{Enabled: true, Script: script, Timeout: time.Second})
	if err == nil {
		t.Fatal("expected timeout")
	}
	data, err := os.ReadFile(pidfile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}
	// Allow the OS to reap the terminated orphan. Cleanup bounds a failing test.
	t.Cleanup(func() {
		if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
			return
		}
	})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("descendant %d survived timeout", pid)
}
