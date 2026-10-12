//go:build windows

package sessions

import (
	"errors"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const helperName = "myrdp-session.exe"

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procGetWindow                = user32.NewProc("GetWindow")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
)

const (
	gwOwner = 4

	swpNoActivate     = 0x0010
	swpShowWindow     = 0x0040
	swpHideWindow     = 0x0080
	swpAsyncWindowPos = 0x4000
)

var (
	enumCallback = syscall.NewCallback(enumProc)
	enumResult   uintptr
)

func enumProc(hwnd, lparam uintptr) uintptr {
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid != windows.GetCurrentProcessId() {
		return 1
	}
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		return 1
	}
	if owner, _, _ := procGetWindow.Call(hwnd, gwOwner); owner != 0 {
		return 1
	}
	enumResult = hwnd
	return 0 // stop enumerating
}

// mainWindow returns this process's visible top-level window, which the
// session windows are embedded into.
func mainWindow() (uintptr, error) {
	enumResult = 0
	procEnumWindows.Call(enumCallback, 0)
	if enumResult == 0 {
		return 0, errors.New("main window not found")
	}
	return enumResult, nil
}

// placeWindow positions a session window above the web view. It is async so
// an unresponsive helper can't block the UI.
func placeWindow(hwnd uintptr, b Bounds, visible bool) {
	flags := uintptr(swpNoActivate | swpAsyncWindowPos)
	if visible {
		flags |= swpShowWindow
	} else {
		flags |= swpHideWindow
	}
	procSetWindowPos.Call(hwnd, 0 /* HWND_TOP */, uintptr(b.X), uintptr(b.Y), uintptr(b.W), uintptr(b.H), flags)
}

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
