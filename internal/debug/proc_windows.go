//go:build windows

package debug

import "os/exec"

func setProcessGroup(cmd *exec.Cmd) {}

func killProcess(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
