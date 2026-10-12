//go:build !windows

package sessions

import (
	"errors"
	"os/exec"
)

// The Linux backend (FreeRDP embedded with /parent-window) is not built yet.
const helperName = "myrdp-session"

func mainWindow() (uintptr, error) {
	return 0, errors.New("sessions are not supported on this platform yet")
}

func placeWindow(hwnd uintptr, b Bounds, visible bool) {}

func hideConsole(cmd *exec.Cmd) {}
