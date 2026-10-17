package main

import (
	"os/exec"
	"syscall"
)

// PowerShell selectors and Ollama imports must not flash a console behind the
// native app. Their native dialogs remain visible.
func hideChildConsole(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: 0x08000000}
}
