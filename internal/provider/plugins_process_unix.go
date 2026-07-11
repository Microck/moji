//go:build unix

package provider

import (
	"os/exec"
	"syscall"
)

func configurePluginCommand(command *exec.Cmd) {
	// A separate process group lets cancellation and output-limit enforcement
	// stop descendants that inherited the plugin's stdout pipe.
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error { return terminatePluginCommand(command) }
}

func terminatePluginCommand(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}
