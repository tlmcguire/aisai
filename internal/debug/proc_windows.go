//go:build windows

package debug

import (
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup hides the console window a console program would open.
// Windows has no process groups in the Unix sense; killProcess ends the tree.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
}

// killProcess ends the program and everything it started. Process.Kill ends
// only the process itself, which would leave a child the program spawned (a
// server, a worker) running after Stop. taskkill /T walks the process tree:
// https://learn.microsoft.com/windows-server/administration/windows-commands/taskkill
func killProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid)).Run()
	_ = cmd.Process.Kill()
}
