package tmux

import (
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func nativeFixture(t *testing.T) (*Client, NativeSize) {
	t.Helper()
	bin := disposableTmux(t)
	runDisposable(t, bin, "new-session", "-d", "-s", "native", "-x", "160", "-y", "45", "cat")
	c := &Client{bin: bin}
	fields := strings.Fields(runDisposable(t, bin, "display-message", "-p", "-t", "native", "#{session_id} #{window_id} #{pane_id} #{pane_pid}"))
	return c, NativeSize{Session: fields[0], Window: fields[1], Pane: fields[2], PID: fields[3], Width: 180, Height: 70}
}

func assertNativeSize(t *testing.T, c *Client, s NativeSize, want string) {
	t.Helper()
	got := strings.TrimSpace(runDisposable(t, c.bin, "display-message", "-p", "-t", s.Pane, "#{pane_width}x#{pane_height}"))
	if got != want {
		t.Fatalf("size %s, want %s", got, want)
	}
}

func TestNativeSizeGrowShrinkReleaseAndOwnership(t *testing.T) {
	c, s := nativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n := NewNativeSizer(c)
	other := NewNativeSizer(c)
	if err := n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "180x70")
	s.Height = 60
	if err := other.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "180x70")
	if err := n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "180x60")
	if err := n.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "160x45")
	if got := strings.TrimSpace(runDisposable(t, c.bin, "show-options", "-wqv", "-t", s.Window, "window-size")); got != "" {
		t.Fatalf("lost inheritance: %q", got)
	}
	if err := other.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "180x60")
	if err := other.Close(ctx); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "160x45")
	if err := other.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "160x45")
}

func TestNativeSizePreservesManualPolicyAndRefusesUnsafeTargets(t *testing.T) {
	for _, kind := range []string{"monitor", "wrong-pid", "split", "linked", "manual"} {
		t.Run(kind, func(t *testing.T) {
			c, s := nativeFixture(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			switch kind {
			case "monitor":
				c.SetMonitorOnly(true)
			case "wrong-pid":
				s.PID = "99999999"
			case "split":
				runDisposable(t, c.bin, "split-window", "-d", "-t", s.Pane, "cat")
			case "linked":
				runDisposable(t, c.bin, "new-session", "-d", "-s", "other", "cat")
				runDisposable(t, c.bin, "link-window", "-s", s.Window, "-t", "other:1")
			case "manual":
				runDisposable(t, c.bin, "set-option", "-w", "-t", s.Window, "window-size", "manual")
			}
			before := runDisposable(t, c.bin, "display-message", "-p", "-t", s.Pane, "#{pane_width}x#{pane_height}")
			n := NewNativeSizer(c)
			if err := n.Sync(ctx, []NativeSize{s}); err != nil {
				t.Fatal(err)
			}
			if kind == "manual" {
				assertNativeSize(t, c, s, "180x70")
			} else {
				assertNativeSize(t, c, s, strings.TrimSpace(before))
			}
			if err := n.Close(ctx); err != nil {
				t.Fatal(err)
			}
			if kind == "manual" {
				assertNativeSize(t, c, s, "160x45")
				if got := strings.TrimSpace(runDisposable(t, c.bin, "show-options", "-wqv", "-t", s.Window, "window-size")); got != "manual" {
					t.Fatal(got)
				}
			}
		})
	}
}

func TestNativeSizeDirectAttachmentHandoff(t *testing.T) {
	c, s := nativeFixture(t)
	runDisposable(t, c.bin, "set-option", "-t", s.Session, "status", "off")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n := NewNativeSizer(c)
	if err := n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, c.bin, "-C", "attach-session", "-t", s.Session)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := input.Close(); err != nil {
			t.Log(err)
		}
		if err := cmd.Wait(); err != nil {
			t.Log(err)
		}
	}()
	if _, err = io.WriteString(input, "refresh-client -C 120,55\n"); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if strings.TrimSpace(runDisposable(t, c.bin, "display-message", "-p", "-t", s.Pane, "#{session_attached}")) == "1" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err = n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(runDisposable(t, c.bin, "show-options", "-wqv", "-t", s.Window, "window-size")); got != "" {
		t.Fatalf("policy not restored on attach: %q", got)
	}
	if got := strings.TrimSpace(runDisposable(t, c.bin, "show-options", "-wqv", "-t", s.Window, nativeOwner)); got != "" {
		t.Fatalf("ownership not released: %q", got)
	}
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); {
		if strings.TrimSpace(runDisposable(t, c.bin, "display-message", "-p", "-t", s.Pane, "#{pane_width}x#{pane_height}")) == "120x55" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	assertNativeSize(t, c, s, "120x55")
	// An attached client is never reacquired, even with stale UI targets.
	if err = n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	if len(n.leases) != 0 {
		t.Fatal("reacquired attached terminal")
	}
	if err = n.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSizeOptInDoesNotEnableKeys(t *testing.T) {
	c, s := nativeFixture(t)
	c.SetMonitorOnly(true)
	c.SetNativeSizeAllowed(true)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	n := NewNativeSizer(c)
	if err := n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	assertNativeSize(t, c, s, "180x70")
	if err := c.SendLiteralKeys(ctx, s.Pane, "must not send"); err == nil {
		t.Fatal("native sizing enabled key forwarding")
	}
	if err := n.Close(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestNativeSizeReleaseAfterWorkerRemoval(t *testing.T) {
	c, s := nativeFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runDisposable(t, c.bin, "new-session", "-d", "-s", "keep-server", "cat")
	n := NewNativeSizer(c)
	if err := n.Sync(ctx, []NativeSize{s}); err != nil {
		t.Fatal(err)
	}
	runDisposable(t, c.bin, "kill-session", "-t", s.Session)
	if err := n.Sync(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if len(n.leases) != 0 {
		t.Fatal("removed worker retained lease")
	}
	if err := n.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
