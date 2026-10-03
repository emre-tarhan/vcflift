//go:build windows

package subprocess

import (
	"os/exec"
	"syscall"
)

// createNoWindow mirrors the Win32 CREATE_NO_WINDOW flag. The GUI executable
// is built with -H windowsgui and has no console of its own; without this flag
// every console child (bcftools.exe, java.exe) allocates a visible console
// window for the duration of the call.
const createNoWindow = 0x08000000

// HideConsole prevents the child from opening a console window on Windows.
func HideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNoWindow,
	}
}
