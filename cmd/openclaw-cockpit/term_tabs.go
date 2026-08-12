//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd

package main

import (
	"os"

	"golang.org/x/sys/unix"
)

// disableHardTabOptimization nudges Bubble Tea away from hard-tab cursor
// movement. In dense bordered grids, literal tab cells can leave artifacts in
// tmux and terminal captures. The original terminal mode is restored on exit.
func disableHardTabOptimization() func() {
	fd := int(os.Stdin.Fd())
	original, err := getTermios(fd)
	if err != nil {
		return func() {}
	}

	next := *original
	next.Oflag &^= unix.TABDLY
	next.Oflag |= unix.TAB3
	if next.Oflag == original.Oflag {
		return func() {}
	}
	if err := setTermios(fd, &next); err != nil {
		return func() {}
	}

	return func() {
		// Best-effort restore on the way out: the process is exiting, there is
		// no recovery path, and reporting here would race the TUI releasing the
		// screen. A failure leaves only the tab-expansion bit set on a terminal
		// we are about to stop using.
		_ = setTermios(fd, original) //nolint:errcheck // justified exit-path discard, rationale above
	}
}
