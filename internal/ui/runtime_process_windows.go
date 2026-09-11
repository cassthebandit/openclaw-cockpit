package ui

import "os/exec"

// CommandContext kills the direct process; WaitDelay bounds inherited pipes.
func configureRuntimeProcess(cmd *exec.Cmd) func() { return func() {} }
