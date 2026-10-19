//go:build !windows && !linux

package sessions

import (
	"errors"
	"os/exec"
)

// Sessions are embedded on Windows and Linux only.
const helperName = "myrdp-session"

func mainWindow() (uintptr, error) {
	return 0, errors.New("sessions are not supported on this platform yet")
}

func placeWindow(hwnd uintptr, b Bounds, visible bool) {}

func hideConsole(cmd *exec.Cmd) {}
