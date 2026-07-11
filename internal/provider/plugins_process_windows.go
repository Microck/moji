//go:build windows

package provider

import (
	"os/exec"
	"strconv"
	"syscall"
)

func configurePluginCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	command.Cancel = func() error { return terminatePluginCommand(command) }
}

func terminatePluginCommand(command *exec.Cmd) error {
	if command.Process == nil {
		return nil
	}
	// taskkill /T terminates descendants that inherited the plugin's handles.
	if err := exec.Command("taskkill", "/PID", strconv.Itoa(command.Process.Pid), "/T", "/F").Run(); err == nil {
		return nil
	}
	return command.Process.Kill()
}
