// Package sessions runs one helper process per open session and keeps each
// helper's window positioned over its tab in the main window.
package sessions

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"myrdp/internal/session"
)

// Bounds is a rectangle in physical pixels within the main window's client
// area.
type Bounds struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type proc struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	hwnd    uintptr
	bounds  Bounds
	visible bool
}

// Manager owns the running sessions.
type Manager struct {
	mu     sync.Mutex
	procs  map[string]*proc
	nextID int
	// onEvent receives every event from every session. An "exited" event
	// is added when a helper process ends.
	onEvent func(id string, ev session.Event)
}

func NewManager(onEvent func(id string, ev session.Event)) *Manager {
	return &Manager{procs: map[string]*proc{}, onEvent: onEvent}
}

// helperPath finds the session helper, which ships beside the main program.
func helperPath() (string, error) {
	if p := os.Getenv("MYRDP_SESSION_HELPER"); p != "" {
		return p, nil
	}
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Join(filepath.Dir(exe), helperName)
	if _, err := os.Stat(p); err != nil {
		return "", fmt.Errorf("session helper not found at %s", p)
	}
	return p, nil
}

// Open starts a session and returns its ID.
func (m *Manager) Open(cfg session.Config, b Bounds) (string, error) {
	helper, err := helperPath()
	if err != nil {
		return "", err
	}
	parent, err := mainWindow()
	if err != nil {
		return "", err
	}
	cmd := exec.Command(helper, "-stdin",
		"-parent", strconv.FormatUint(uint64(parent), 10),
		"-x", strconv.Itoa(b.X), "-y", strconv.Itoa(b.Y), "-w", strconv.Itoa(b.W), "-h", strconv.Itoa(b.H))
	hideConsole(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start session helper: %w", err)
	}
	// The config carries the password, so it goes over the pipe rather
	// than the command line.
	line, _ := json.Marshal(cfg)
	if _, err := stdin.Write(append(line, '\n')); err != nil {
		cmd.Process.Kill()
		return "", fmt.Errorf("send config to session helper: %w", err)
	}

	m.mu.Lock()
	m.nextID++
	id := "s" + strconv.Itoa(m.nextID)
	p := &proc{cmd: cmd, stdin: stdin, bounds: b, visible: true}
	m.procs[id] = p
	m.mu.Unlock()

	go m.read(id, p, stdout)
	return id, nil
}

func (m *Manager) read(id string, p *proc, stdout io.Reader) {
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		var ev session.Event
		if json.Unmarshal(scanner.Bytes(), &ev) != nil {
			continue
		}
		if ev.Name == "ready" {
			m.mu.Lock()
			p.hwnd = uintptr(ev.Hwnd)
			placeWindow(p.hwnd, p.bounds, p.visible)
			m.mu.Unlock()
		}
		m.onEvent(id, ev)
	}
	p.cmd.Wait()
	m.mu.Lock()
	delete(m.procs, id)
	m.mu.Unlock()
	m.onEvent(id, session.Event{Name: "exited"})
}

// SetBounds moves a session's window and shows or hides it.
func (m *Manager) SetBounds(id string, b Bounds, visible bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.procs[id]
	if p == nil {
		return
	}
	if p.bounds == b && p.visible == visible {
		return
	}
	p.bounds, p.visible = b, visible
	if p.hwnd != 0 {
		placeWindow(p.hwnd, b, visible)
	}
}

// Command sends an instruction to a session.
func (m *Manager) Command(id, cmd string) error {
	m.mu.Lock()
	p := m.procs[id]
	m.mu.Unlock()
	if p == nil {
		return errors.New("session is not running")
	}
	line, _ := json.Marshal(session.Command{Cmd: cmd})
	_, err := p.stdin.Write(append(line, '\n'))
	return err
}

// Close ends a session, killing the helper if it doesn't exit promptly.
func (m *Manager) Close(id string) {
	m.mu.Lock()
	p := m.procs[id]
	m.mu.Unlock()
	if p == nil {
		return
	}
	if p.hwnd != 0 {
		placeWindow(p.hwnd, p.bounds, false)
	}
	p.stdin.Close() // the helper closes when its stdin ends
	go func() {
		time.Sleep(3 * time.Second)
		m.mu.Lock()
		_, alive := m.procs[id]
		m.mu.Unlock()
		if alive {
			p.cmd.Process.Kill()
		}
	}()
}

// CloseAll ends every session; used at shutdown.
func (m *Manager) CloseAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.procs {
		p.cmd.Process.Kill()
	}
}
