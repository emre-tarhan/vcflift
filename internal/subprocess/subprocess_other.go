//go:build !windows

package subprocess

import "os/exec"

// HideConsole is a no-op on platforms without console windows.
func HideConsole(cmd *exec.Cmd) {}
