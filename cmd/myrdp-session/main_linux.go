//go:build linux

// myrdp-session hosts a single RDP session in its own process by running
// FreeRDP embedded in the window the manager hands it. It talks to the
// manager over stdio like the Windows helper: events go out as JSON lines on
// stdout, commands come in as JSON lines on stdin.
package main

/*
#cgo pkg-config: x11
#include <X11/Xlib.h>
#include <X11/Xatom.h>

static int ignore_errors(Display *d, XErrorEvent *e) { return 0; }

// open_display ignores X errors: the FreeRDP window can go away at any time.
static Display *open_display(void) {
	XSetErrorHandler(ignore_errors);
	return XOpenDisplay(NULL);
}

static Window root_window(Display *d) { return DefaultRootWindow(d); }

static int has_pid(Display *d, Window w, Atom prop, unsigned long pid) {
	Atom type;
	int format;
	unsigned long count, after;
	unsigned char *data = NULL;
	int match = 0;
	if (XGetWindowProperty(d, w, prop, 0, 1, False, XA_CARDINAL, &type, &format, &count, &after, &data) == Success && data != NULL) {
		match = count == 1 && format == 32 && *(unsigned long *)data == pid;
		XFree(data);
	}
	return match;
}

// find_window returns the window of process pid: a child of parent, or with
// parent 0 one of the window manager's top-level windows.
static Window find_window(Display *d, Window parent, unsigned long pid) {
	Atom prop = XInternAtom(d, "_NET_WM_PID", True);
	if (prop == None) {
		return 0;
	}
	Window found = 0;
	if (parent != 0) {
		Window root, up, *kids = NULL;
		unsigned int n = 0;
		if (XQueryTree(d, parent, &root, &up, &kids, &n)) {
			for (unsigned int i = 0; i < n && found == 0; i++) {
				if (has_pid(d, kids[i], prop, pid)) {
					found = kids[i];
				}
			}
			XFree(kids);
		}
		return found;
	}
	Atom list = XInternAtom(d, "_NET_CLIENT_LIST", True);
	Atom type;
	int format;
	unsigned long count, after;
	unsigned char *data = NULL;
	if (list != None && XGetWindowProperty(d, DefaultRootWindow(d), list, 0, 4096, False, XA_WINDOW, &type, &format, &count, &after, &data) == Success && data != NULL) {
		Window *ws = (Window *)data;
		for (unsigned long i = 0; i < count && found == 0; i++) {
			if (has_pid(d, ws[i], prop, pid)) {
				found = ws[i];
			}
		}
		XFree(data);
	}
	return found;
}

static void focus_window(Display *d, Window w) {
	XSetInputFocus(d, w, RevertToParent, CurrentTime);
	XFlush(d);
}
*/
import "C"

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"myrdp/internal/freerdp"
	"myrdp/internal/session"
)

var (
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

// exitMessages explains FreeRDP's exit codes (xfreerdp's XF_EXIT_* values).
var exitMessages = map[int]string{
	0:   "The session ended.",
	1:   "The session was disconnected from the server.",
	2:   "The session was logged off from the server.",
	3:   "The session was idle for too long.",
	4:   "The logon timed out.",
	5:   "Another connection took over this session.",
	6:   "The server ran out of memory.",
	7:   "The server refused the connection.",
	9:   "This account is not allowed to connect to the server.",
	131: "Could not connect to the server.",
	132: "Authentication failed.",
	133: "The client and server could not agree on security settings.",
	134: "The logon failed.",
	135: "The account is locked out.",
	139: "The host name could not be looked up.",
	140: "The host name could not be found.",
	141: "Could not connect to the server.",
	143: "The secure connection failed. With \"Do not connect\", this includes a certificate that cannot be verified.",
	144: "This account is not allowed to connect to the server.",
	147: "The connection to the server failed.",
	148: "The password has expired.",
	149: "The password must be changed before logging on.",
	150: "The domain controller (KDC) could not be reached.",
	151: "The account is disabled.",
	152: "The password has expired.",
	153: "The account has been revoked.",
	154: "Wrong username or password.",
	155: "Access was denied.",
	156: "The account is restricted.",
	157: "The account has expired.",
	158: "This account is not allowed this type of logon.",
	159: "No credentials were given, or some are missing.",
}

// authFailures are the exit codes for credentials that were refused.
var authFailures = map[int]bool{132: true, 134: true, 154: true, 159: true}

// errorName picks FreeRDP's error names out of its log, such as
// ERRCONNECT_DNS_NAME_NOT_FOUND.
var errorName = regexp.MustCompile(`\b(ERRCONNECT|ERRINFO)_[A-Z0-9_]+`)

func main() {
	// FreeRDP is told to stop when the thread that started it ends, so that
	// thread must live as long as the process.
	runtime.LockOSThread()

	parent := flag.Uint64("parent", 0, "X11 window to embed into")
	w := flag.Int("w", 0, "width within the parent window")
	h := flag.Int("h", 0, "height within the parent window")
	flag.Int("x", 0, "left edge within the parent window (placed by the manager)")
	flag.Int("y", 0, "top edge within the parent window (placed by the manager)")
	useStdin := flag.Bool("stdin", false, "read a JSON config from the first line of stdin, then commands")
	flag.Parse()
	if !*useStdin {
		fail("myrdp-session is started by myrdp, with -stdin")
	}

	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	if !in.Scan() {
		fail("no config on stdin")
	}
	var cfg session.Config
	if err := json.Unmarshal(in.Bytes(), &cfg); err != nil {
		fail("bad config: %v", err)
	}

	program, err := freerdp.Find()
	if err != nil {
		fail("%v", err)
	}
	display := C.open_display()
	if display == nil {
		fail("cannot open the X display: sessions need X11 or XWayland")
	}
	into := *parent
	if cfg.Fullscreen {
		into = 0
	}

	// The options, password included, go over a pipe rather than the
	// command line. Closing it afterwards also means FreeRDP can't wait on a
	// prompt nobody can see.
	cmd := exec.Command(program, "/args-from:stdin")
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM}
	cmd.Stdin = strings.NewReader(strings.Join(freerdp.Args(cfg, into, *w, *h), "\n") + "\n")
	logR, logW, err := os.Pipe()
	if err != nil {
		fail("%v", err)
	}
	cmd.Stdout, cmd.Stderr = logW, logW

	emit(session.Event{Name: "connecting"})
	if err := cmd.Start(); err != nil {
		fail("start %s: %v", program, err)
	}
	logW.Close()
	pid := C.ulong(cmd.Process.Pid)

	var (
		mu       sync.Mutex
		lastErr  string
		window   C.Window
		stopping bool
	)

	logDone := make(chan struct{})
	go func() {
		defer close(logDone)
		scan := bufio.NewScanner(logR)
		for scan.Scan() {
			if name := errorName.FindString(scan.Text()); name != "" {
				mu.Lock()
				lastErr = name
				mu.Unlock()
			}
		}
	}()

	exited := make(chan struct{})
	// FreeRDP's window appears once it has connected; that is the signal
	// the session is up, and the window is what the manager places.
	go func() {
		tick := time.NewTicker(100 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-exited:
				return
			case <-tick.C:
			}
			mu.Lock()
			win := C.find_window(display, C.Window(into), pid)
			if win != 0 {
				window = win
			}
			mu.Unlock()
			if win != 0 {
				ready := session.Event{Name: "ready"}
				if into != 0 {
					ready.Hwnd = uint64(win)
				}
				emit(ready)
				emit(session.Event{Name: "loginComplete"})
				return
			}
		}
	}()

	stop := func() {
		mu.Lock()
		stopping = true
		mu.Unlock()
		cmd.Process.Signal(syscall.SIGTERM)
		go func() {
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				cmd.Process.Kill()
			}
		}()
	}

	go func() {
		for in.Scan() {
			var c session.Command
			if json.Unmarshal(in.Bytes(), &c) != nil {
				continue
			}
			switch c.Cmd {
			case "disconnect", "close":
				stop()
			case "focus":
				mu.Lock()
				if window != 0 {
					C.focus_window(display, window)
				}
				mu.Unlock()
			case "fullscreen":
				emit(session.Event{Name: "log", Message: "Full screen can't be switched on for an embedded session on Linux."})
			}
		}
		stop() // the manager closes stdin to end the session
	}()

	err = cmd.Wait()
	close(exited)
	<-logDone
	code := 0
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code = exitErr.ExitCode()
	} else if err != nil && !errors.Is(err, io.EOF) {
		fail("FreeRDP: %v", err)
	}

	mu.Lock()
	message := exitMessages[code]
	switch {
	case stopping:
		message = "Disconnected."
	case message == "" && lastErr != "":
		message = fmt.Sprintf("FreeRDP stopped with code %d (%s).", code, lastErr)
	case message == "":
		message = fmt.Sprintf("FreeRDP stopped with code %d.", code)
	}
	if cfg.Password == "" && !stopping && authFailures[code] {
		message += " No password is saved for this connection."
	}
	mu.Unlock()
	emit(session.Event{Name: "disconnected", Reason: int32(code), Message: message})
}
