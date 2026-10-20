package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/menu"
	"github.com/wailsapp/wails/v2/pkg/menu/keys"
	"github.com/wailsapp/wails/v2/pkg/runtime"

	"myrdp/internal/config"
	"myrdp/internal/core"
	"myrdp/internal/secrets"
	"myrdp/internal/session"
	"myrdp/internal/sessions"
	"myrdp/internal/store"
)

const version = "0.1.0"

// App is the API the frontend calls.
type App struct {
	ctx context.Context

	connectionsPath string
	settingsPath    string
	settings        *config.Settings
	svc             *core.Service
	sessions        *sessions.Manager
	// startupErr is shown by the frontend instead of the normal UI, for
	// example when the connections file can't be read.
	startupErr string

	mu sync.Mutex
	// unsaved names the settings tabs holding changes that haven't been
	// saved, as last reported by the frontend.
	unsaved []string
	// exiting is set once the user has agreed to exit, so they aren't
	// asked twice.
	exiting bool
}

// SessionInfo describes a session that was just started.
type SessionInfo struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	NodeID string `json:"nodeId"`
}

// SessionEvent is emitted to the frontend as the "session" event.
type SessionEvent struct {
	ID    string        `json:"id"`
	Event session.Event `json:"event"`
}

// AppInfo is shown on the program settings tab.
type AppInfo struct {
	ConnectionsPath string    `json:"connectionsPath"`
	SettingsPath    string    `json:"settingsPath"`
	StorageType     string    `json:"storageType"`
	UI              config.UI `json:"ui"`
	StartupError    string    `json:"startupError"`
}

// NewApp loads settings and the connection tree. dir overrides the config
// directory when non-empty.
func NewApp(dir string) *App {
	a := &App{}
	if dir == "" {
		var err error
		if dir, err = config.Dir(); err != nil {
			a.startupErr = "Cannot find the config directory: " + err.Error()
			a.settings, _ = config.Load("")
			return a
		}
	}
	a.connectionsPath = filepath.Join(dir, "connections.json")
	a.settingsPath = filepath.Join(dir, "settings.json")

	var err error
	if a.settings, err = config.Load(a.settingsPath); err != nil {
		a.startupErr = "Cannot read " + a.settingsPath + ": " + err.Error()
		return a
	}
	if a.svc, err = core.New(store.NewJSONFile(a.connectionsPath), secrets.Keyring{}); err != nil {
		a.startupErr = "Cannot open the connections file: " + err.Error()
	}
	return a
}

func (a *App) startup(ctx context.Context) {
	a.ctx = ctx
	a.sessions = sessions.NewManager(func(id string, ev session.Event) {
		runtime.EventsEmit(ctx, "session", SessionEvent{ID: id, Event: ev})
	})
}

// SetUnsaved is called by the frontend whenever the set of settings tabs
// with unsaved changes changes.
func (a *App) SetUnsaved(names []string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unsaved = names
}

// confirmExit reports whether the program may close. With unsaved changes
// it asks first.
func (a *App) confirmExit() bool {
	a.mu.Lock()
	names, exiting := a.unsaved, a.exiting
	a.mu.Unlock()
	if exiting || len(names) == 0 {
		return true
	}
	answer, err := runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
		Type:          runtime.QuestionDialog,
		Title:         "Unsaved changes",
		Message:       "These settings have not been saved:\n\n    " + strings.Join(names, "\n    ") + "\n\nExit without saving them?",
		DefaultButton: "No",
	})
	if err != nil || answer != "Yes" {
		return false
	}
	a.mu.Lock()
	a.exiting = true
	a.mu.Unlock()
	return true
}

// beforeClose runs when the window is asked to close; returning true keeps
// it open.
func (a *App) beforeClose(context.Context) bool {
	return !a.confirmExit()
}

func (a *App) shutdown(context.Context) {
	if a.sessions != nil {
		a.sessions.CloseAll()
	}
}

func (a *App) service() (*core.Service, error) {
	if a.svc == nil {
		return nil, errors.New(a.startupErr)
	}
	return a.svc, nil
}

func (a *App) GetAppInfo() AppInfo {
	return AppInfo{
		ConnectionsPath: a.connectionsPath,
		SettingsPath:    a.settingsPath,
		StorageType:     a.settings.Storage.Type,
		UI:              a.settings.UI,
		StartupError:    a.startupErr,
	}
}

// SaveUI remembers the sidebar layout.
func (a *App) SaveUI(ui config.UI) error {
	if a.settingsPath == "" {
		return nil
	}
	a.settings.UI = ui
	return a.settings.Save(a.settingsPath)
}

func (a *App) GetTree() ([]*core.TreeNode, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Tree(), nil
}

func (a *App) RenameNode(id, name string) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.Rename(id, name)
}

func (a *App) DeleteNode(id string) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.Delete(id)
}

func (a *App) MoveNode(id, parentID string, index int) error {
	svc, err := a.service()
	if err != nil {
		return err
	}
	return svc.Move(id, parentID, index)
}

func (a *App) DuplicateNode(id string) (*core.TreeNode, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Duplicate(id)
}

func (a *App) GetSettings(id string) (*core.NodeSettings, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Settings(id)
}

// GetDraftSettings returns the starting settings for a node that has not
// been created yet.
func (a *App) GetDraftSettings(parentID, nodeType string) (*core.NodeSettings, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.DraftSettings(parentID, nodeType)
}

// SaveNode applies everything changed in a settings tab, creating the node
// if it is new.
func (a *App) SaveNode(req core.SaveRequest) (*core.TreeNode, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	return svc.Save(req)
}

// Connect opens a session to a saved connection.
func (a *App) Connect(nodeID string, b sessions.Bounds) (*SessionInfo, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	cfg, err := svc.SessionConfig(nodeID)
	if err != nil {
		return nil, err
	}
	id, err := a.sessions.Open(cfg, b)
	if err != nil {
		return nil, a.showMissing(err)
	}
	return &SessionInfo{ID: id, Title: cfg.Name, NodeID: nodeID}, nil
}

// QuickConnect opens a session to a saved connection with this name, or
// failing that to "host" or "host:port" using the default settings.
func (a *App) QuickConnect(target string, b sessions.Bounds) (*SessionInfo, error) {
	svc, err := a.service()
	if err != nil {
		return nil, err
	}
	if id := svc.FindByName(target); id != "" {
		return a.Connect(id, b)
	}
	cfg, err := svc.QuickConfig(target)
	if err != nil {
		return nil, err
	}
	id, err := a.sessions.Open(cfg, b)
	if err != nil {
		return nil, a.showMissing(err)
	}
	return &SessionInfo{ID: id, Title: cfg.Name}, nil
}

// showMissing opens a message box when something the program needs isn't
// installed. The error is still returned, so it is logged in Messages too.
func (a *App) showMissing(err error) error {
	var missing *sessions.MissingError
	if errors.As(err, &missing) {
		runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
			Type:    runtime.ErrorDialog,
			Title:   "Missing software",
			Message: missing.Error(),
		})
	}
	return err
}

func (a *App) SetSessionBounds(id string, b sessions.Bounds, visible bool) {
	a.sessions.SetBounds(id, b, visible)
}

func (a *App) SessionCommand(id, cmd string) error {
	return a.sessions.Command(id, cmd)
}

func (a *App) CloseSession(id string) {
	a.sessions.Close(id)
}

// menu builds the native menu bar. Most items are handled by the frontend,
// which receives the action name as a "menu" event.
func (a *App) menu() *menu.Menu {
	send := func(action string) menu.Callback {
		return func(*menu.CallbackData) { runtime.EventsEmit(a.ctx, "menu", action) }
	}
	m := menu.NewMenu()

	file := m.AddSubmenu("File")
	file.AddText("New Connection", keys.CmdOrCtrl("n"), send("new-connection"))
	file.AddText("New Folder", keys.Combo("n", keys.CmdOrCtrlKey, keys.ShiftKey), send("new-folder"))
	file.AddSeparator()
	file.AddText("Exit", nil, func(*menu.CallbackData) {
		if a.confirmExit() {
			runtime.Quit(a.ctx)
		}
	})

	view := m.AddSubmenu("View")
	view.AddText("Connection List", keys.CmdOrCtrl("b"), send("toggle-sidebar"))
	view.AddText("Messages", keys.CmdOrCtrl("m"), send("toggle-messages"))

	settings := m.AddSubmenu("Settings")
	settings.AddText("Program Settings", keys.CmdOrCtrl(","), send("app-settings"))
	settings.AddText("Default Connection Settings", nil, send("default-settings"))

	help := m.AddSubmenu("Help")
	help.AddText("About myrdp", nil, func(*menu.CallbackData) {
		runtime.MessageDialog(a.ctx, runtime.MessageDialogOptions{
			Type:  runtime.InfoDialog,
			Title: "About myrdp",
			Message: "myrdp " + version + "\n\nA tabbed remote desktop manager.\n\n" +
				"Free software, licensed under the GNU General Public License version 3 or later.",
		})
	})
	return m
}
