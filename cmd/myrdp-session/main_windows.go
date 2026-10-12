//go:build windows

// myrdp-session hosts a single RDP session in its own process. The manager
// starts one per tab, hands it a parent window handle, and talks to it over
// stdio: events go out as JSON lines on stdout, commands come in as JSON
// lines on stdin.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"golang.org/x/sys/windows"

	"myrdp/internal/rdpax"
	"myrdp/internal/session"
)

const (
	wmDestroy  = 0x0002
	wmSize     = 0x0005
	wmSetFocus = 0x0007
	wmClose    = 0x0010
	wmTimer    = 0x0113
	wmCommands = 0x8000 + 1 // WM_APP+1: commands are waiting on the channel

	wsChild            = 0x40000000
	wsVisible          = 0x10000000
	wsClipChildren     = 0x02000000
	wsOverlappedWindow = 0x00CF0000
	cwUseDefault       = 0x80000000

	timerResize = 1
	timerQuit   = 2

	resizeDebounceMs = 250
)

var (
	user32                            = windows.NewLazySystemDLL("user32.dll")
	procRegisterClassExW              = user32.NewProc("RegisterClassExW")
	procCreateWindowExW               = user32.NewProc("CreateWindowExW")
	procDefWindowProcW                = user32.NewProc("DefWindowProcW")
	procDestroyWindow                 = user32.NewProc("DestroyWindow")
	procPostQuitMessage               = user32.NewProc("PostQuitMessage")
	procPostMessageW                  = user32.NewProc("PostMessageW")
	procGetMessageW                   = user32.NewProc("GetMessageW")
	procTranslateMessage              = user32.NewProc("TranslateMessage")
	procDispatchMessageW              = user32.NewProc("DispatchMessageW")
	procGetClientRect                 = user32.NewProc("GetClientRect")
	procSetTimer                      = user32.NewProc("SetTimer")
	procKillTimer                     = user32.NewProc("KillTimer")
	procLoadCursorW                   = user32.NewProc("LoadCursorW")
	procGetSystemMetrics              = user32.NewProc("GetSystemMetrics")
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

type rect struct{ left, top, right, bottom int32 }

var (
	host     windows.HWND
	client   *rdpax.Client
	commands = make(chan session.Command, 16)

	// autoResize is set when the remote desktop should follow the window size.
	autoResize bool

	outMu sync.Mutex
	out   = json.NewEncoder(os.Stdout)
)

func emit(ev session.Event) {
	outMu.Lock()
	defer outMu.Unlock()
	out.Encode(ev)
}

func fail(format string, args ...interface{}) {
	emit(session.Event{Name: "error", Message: fmt.Sprintf(format, args...)})
	os.Exit(1)
}

func clientSize(hwnd windows.HWND) (int32, int32) {
	var r rect
	procGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&r)))
	return r.right - r.left, r.bottom - r.top
}

func wndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmSize:
		if client != nil {
			client.Move(int32(lParam&0xFFFF), int32((lParam>>16)&0xFFFF))
			if autoResize {
				procSetTimer.Call(uintptr(hwnd), timerResize, resizeDebounceMs, 0)
			}
		}
		return 0
	case wmTimer:
		procKillTimer.Call(uintptr(hwnd), wParam)
		switch wParam {
		case timerResize:
			if client != nil {
				w, h := clientSize(hwnd)
				if err := client.UpdateDisplay(w, h); err != nil {
					emit(session.Event{Name: "log", Message: "resize: " + err.Error()})
				}
			}
		case timerQuit:
			emit(session.Event{Name: "timeout"})
			procDestroyWindow.Call(uintptr(hwnd))
		}
		return 0
	case wmSetFocus:
		if client != nil {
			client.Focus()
		}
		return 0
	case wmCommands:
		for {
			select {
			case c := <-commands:
				runCommand(hwnd, c)
			default:
				return 0
			}
		}
	case wmDestroy:
		// Release the control while its window still exists; child windows
		// are destroyed after this message returns.
		if client != nil {
			client.Close()
			client = nil
		}
		procPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

func runCommand(hwnd windows.HWND, c session.Command) {
	switch c.Cmd {
	case "disconnect":
		if client != nil && client.Connected() {
			client.Disconnect() // the disconnected event closes the window
			return
		}
		procDestroyWindow.Call(uintptr(hwnd))
	case "close":
		procDestroyWindow.Call(uintptr(hwnd))
	case "fullscreen":
		if client != nil {
			if err := client.ToggleFullScreen(); err != nil {
				emit(session.Event{Name: "log", Message: "full screen: " + err.Error()})
			}
		}
	case "focus":
		if client != nil {
			client.Focus()
		}
	default:
		emit(session.Event{Name: "log", Message: "unknown command: " + c.Cmd})
	}
}

func onEvent(ev session.Event) {
	emit(ev)
	if ev.Name == "disconnected" || ev.Name == "fatalError" {
		// Don't tear the control down from inside its own callback.
		procPostMessageW.Call(uintptr(host), wmClose, 0, 0)
	}
}

type bounds struct{ x, y, w, h int }

func createHostWindow(parent windows.HWND, title string, b bounds) (windows.HWND, error) {
	className, _ := windows.UTF16PtrFromString("MyRdpSessionHost")
	cursor, _, _ := procLoadCursorW.Call(0, 32512) // IDC_ARROW
	wc := wndClassEx{
		wndProc:   syscall.NewCallback(wndProc),
		cursor:    windows.Handle(cursor),
		className: className,
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return 0, fmt.Errorf("RegisterClassEx: %w", err)
	}

	titlePtr, _ := windows.UTF16PtrFromString(title)
	style := uintptr(wsOverlappedWindow | wsVisible | wsClipChildren)
	x, y, w, h := uintptr(cwUseDefault), uintptr(cwUseDefault), uintptr(1280), uintptr(800)
	if parent != 0 {
		style = wsChild | wsVisible | wsClipChildren
		if b.w <= 0 || b.h <= 0 {
			pw, ph := clientSize(parent)
			b = bounds{0, 0, int(pw), int(ph)}
		}
		x, y, w, h = uintptr(b.x), uintptr(b.y), uintptr(b.w), uintptr(b.h)
	}
	hwnd, _, err := procCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(titlePtr)),
		style, x, y, w, h, uintptr(parent), 0, 0, 0)
	if hwnd == 0 {
		return 0, fmt.Errorf("CreateWindowEx: %w", err)
	}
	return windows.HWND(hwnd), nil
}

func main() {
	// COM STA and the window message loop are bound to this thread.
	runtime.LockOSThread()

	// Flags cover standalone use; the manager sends the full config on stdin.
	cfg := session.Config{Port: 3389, AutoResize: true, ColorDepth: 32, AutoReconnect: true, NLA: true,
		RedirectClipboard: true, Sound: "local", Speed: "auto"}
	var b bounds
	flag.StringVar(&cfg.Host, "host", "", "server to connect to")
	flag.IntVar(&cfg.Port, "port", cfg.Port, "server port")
	flag.StringVar(&cfg.Username, "user", "", "username")
	flag.StringVar(&cfg.Domain, "domain", "", "domain")
	flag.StringVar(&cfg.AuthLevel, "auth-level", "warn", "if server authentication fails: none, warn or required")
	parent := flag.Uint64("parent", 0, "window handle to embed into; 0 opens a standalone window")
	flag.IntVar(&b.x, "x", 0, "left edge within the parent window")
	flag.IntVar(&b.y, "y", 0, "top edge within the parent window")
	flag.IntVar(&b.w, "w", 0, "width within the parent window")
	flag.IntVar(&b.h, "h", 0, "height within the parent window")
	useStdin := flag.Bool("stdin", false, "read a JSON config from the first line of stdin, then commands")
	timeout := flag.Int("timeout", 0, "exit after this many seconds (for testing)")
	flag.Parse()
	cfg.Password = os.Getenv("MYRDP_PASSWORD")

	var stdin *bufio.Scanner
	if *useStdin {
		stdin = bufio.NewScanner(os.Stdin)
		if !stdin.Scan() {
			fail("no config on stdin")
		}
		cfg = session.Config{}
		if err := json.Unmarshal(stdin.Bytes(), &cfg); err != nil {
			fail("bad config: %v", err)
		}
	}
	if cfg.Name == "" {
		cfg.Name = cfg.Host
	}

	procSetProcessDpiAwarenessContext.Call(^uintptr(3)) // PER_MONITOR_AWARE_V2
	if err := ole.CoInitializeEx(0, ole.COINIT_APARTMENTTHREADED); err != nil {
		fail("CoInitializeEx: %v", err)
	}
	defer ole.CoUninitialize()

	var err error
	host, err = createHostWindow(windows.HWND(*parent), cfg.Name+" - myrdp", b)
	if err != nil {
		fail("%v", err)
	}
	winW, winH := clientSize(host)
	client, err = rdpax.New(host, winW, winH, onEvent)
	if err != nil {
		fail("%v", err)
	}

	// Remote desktop size: the whole screen, a fixed size, or the window.
	w, h := winW, winH
	switch {
	case cfg.Fullscreen:
		sw, _, _ := procGetSystemMetrics.Call(0) // SM_CXSCREEN
		sh, _, _ := procGetSystemMetrics.Call(1) // SM_CYSCREEN
		w, h = int32(sw), int32(sh)
	case cfg.Width > 0 && cfg.Height > 0:
		w, h = int32(cfg.Width), int32(cfg.Height)
	default:
		autoResize = cfg.AutoResize
	}

	warnings, err := client.Apply(cfg, w, h)
	if err != nil {
		fail("%v", err)
	}
	for _, warning := range warnings {
		emit(session.Event{Name: "log", Message: warning})
	}
	emit(session.Event{Name: "ready", Hwnd: uint64(host), Width: w, Height: h})
	if err := client.Connect(); err != nil {
		fail("connect: %v", err)
	}

	if *timeout > 0 {
		procSetTimer.Call(uintptr(host), timerQuit, uintptr(*timeout*1000), 0)
	}
	if stdin != nil {
		go func() {
			for stdin.Scan() {
				var c session.Command
				if json.Unmarshal(stdin.Bytes(), &c) != nil {
					continue
				}
				commands <- c
				procPostMessageW.Call(uintptr(host), wmCommands, 0, 0)
			}
			// The manager went away; don't leave an orphaned session.
			commands <- session.Command{Cmd: "close"}
			procPostMessageW.Call(uintptr(host), wmCommands, 0, 0)
		}()
	}

	var m msg
	for {
		r, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
