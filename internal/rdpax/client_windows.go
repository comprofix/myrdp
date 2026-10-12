//go:build windows

// Package rdpax hosts Microsoft's RDP ActiveX control (mstscax.dll) in a
// child window using the ATL control host that ships with Windows.
//
// Everything here must run on a single OS thread that has initialised COM as
// an STA and pumps window messages.
package rdpax

import (
	"errors"
	"fmt"
	"unsafe"

	ole "github.com/go-ole/go-ole"
	"github.com/go-ole/go-ole/oleutil"
	"golang.org/x/sys/windows"

	"myrdp/internal/session"
)

// CLSIDs of the "NotSafeForScripting" control, newest first. These are the
// variants that allow a password to be supplied programmatically.
var controlCLSIDs = []string{
	"{A0C63C30-F08D-4AB4-907C-34905D770C7D}", // MsRdpClient10 (control version 11)
	"{8B918B82-7985-4C24-89DF-C33AD2BBFBCD}", // MsRdpClient9  (control version 10)
}

var (
	atl                 = windows.NewLazySystemDLL("atl.dll")
	procAtlAxWinInit    = atl.NewProc("AtlAxWinInit")
	procAtlAxGetControl = atl.NewProc("AtlAxGetControl")

	user32              = windows.NewLazySystemDLL("user32.dll")
	procCreateWindowExW = user32.NewProc("CreateWindowExW")
	procDestroyWindow   = user32.NewProc("DestroyWindow")
	procMoveWindow      = user32.NewProc("MoveWindow")
	procSetFocus        = user32.NewProc("SetFocus")
)

const (
	wsChild        = 0x40000000
	wsVisible      = 0x10000000
	wsClipChildren = 0x02000000
)

// Client is one hosted RDP control.
type Client struct {
	hwnd     windows.HWND
	disp     *ole.IDispatch
	adv      *ole.IDispatch
	point    *ole.IConnectionPoint
	cookie   uint32
	sink     *eventSink
	onEvent  func(session.Event)
	loggedIn bool
}

// New creates the control as a child of parent, filling a w x h client area.
func New(parent windows.HWND, w, h int32, onEvent func(session.Event)) (*Client, error) {
	if r, _, err := procAtlAxWinInit.Call(); r == 0 {
		return nil, fmt.Errorf("AtlAxWinInit: %w", err)
	}
	class, _ := windows.UTF16PtrFromString("AtlAxWin")

	c := &Client{onEvent: onEvent}
	var lastErr error
	for _, clsid := range controlCLSIDs {
		name, _ := windows.UTF16PtrFromString(clsid)
		hwnd, _, err := procCreateWindowExW.Call(0,
			uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(name)),
			wsChild|wsVisible|wsClipChildren,
			0, 0, uintptr(w), uintptr(h),
			uintptr(parent), 0, 0, 0)
		if hwnd == 0 {
			lastErr = fmt.Errorf("create control %s: %w", clsid, err)
			continue
		}
		c.hwnd = windows.HWND(hwnd)
		break
	}
	if c.hwnd == 0 {
		return nil, lastErr
	}

	var unk *ole.IUnknown
	if hr, _, _ := procAtlAxGetControl.Call(uintptr(c.hwnd), uintptr(unsafe.Pointer(&unk))); int32(hr) < 0 || unk == nil {
		c.Close()
		return nil, fmt.Errorf("AtlAxGetControl: %w", ole.NewError(hr))
	}
	defer unk.Release()

	disp, err := unk.QueryInterface(ole.IID_IDispatch)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("control IDispatch: %w", err)
	}
	c.disp = disp

	for _, name := range []string{"AdvancedSettings9", "AdvancedSettings8", "AdvancedSettings2"} {
		if v, err := oleutil.GetProperty(c.disp, name); err == nil && v.ToIDispatch() != nil {
			c.adv = v.ToIDispatch()
			break
		}
	}
	if c.adv == nil {
		c.Close()
		return nil, errors.New("control has no AdvancedSettings interface")
	}

	if err := c.advise(); err != nil {
		c.Close()
		return nil, fmt.Errorf("subscribe to events: %w", err)
	}
	return c, nil
}

func (c *Client) advise() error {
	unk, err := c.disp.QueryInterface(ole.IID_IConnectionPointContainer)
	if err != nil {
		return err
	}
	container := (*ole.IConnectionPointContainer)(unsafe.Pointer(unk))
	defer container.Release()

	if err := container.FindConnectionPoint(iidMsTscAxEvents, &c.point); err != nil {
		return err
	}
	c.sink = newEventSink(c.handle)
	cookie, err := c.point.Advise(c.sink.unknown())
	if err != nil {
		return err
	}
	c.cookie = cookie
	return nil
}

func argInt(args []ole.VARIANT, i int) int32 {
	if i >= len(args) {
		return 0
	}
	return int32(args[i].Val)
}

func (c *Client) handle(dispid int32, args []ole.VARIANT) {
	switch dispid {
	case dispOnConnecting:
		c.onEvent(session.Event{Name: "connecting"})
	case dispOnConnected:
		c.onEvent(session.Event{Name: "connected"})
	case dispOnLoginComplete:
		c.loggedIn = true
		c.onEvent(session.Event{Name: "loginComplete"})
	case dispOnDisconnected:
		c.loggedIn = false
		ev := session.Event{Name: "disconnected", Reason: argInt(args, 0)}
		if v, err := oleutil.GetProperty(c.disp, "ExtendedDisconnectReason"); err == nil {
			ev.ExtendedReason = int32(v.Val)
		}
		if v, err := oleutil.CallMethod(c.disp, "GetErrorDescription", ev.Reason, ev.ExtendedReason); err == nil {
			ev.Message = v.ToString()
			v.Clear()
		}
		c.onEvent(ev)
	case dispOnFatalError:
		c.onEvent(session.Event{Name: "fatalError", Reason: argInt(args, 0)})
	case dispOnWarning:
		c.onEvent(session.Event{Name: "warning", Reason: argInt(args, 0)})
	case dispOnLogonError:
		c.onEvent(session.Event{Name: "logonError", Reason: argInt(args, 0)})
	case dispOnRemoteDesktopSizeChange:
		c.onEvent(session.Event{Name: "remoteSize", Width: argInt(args, 0), Height: argInt(args, 1)})
	case dispOnAutoReconnecting2:
		c.onEvent(session.Event{Name: "reconnecting", Reason: argInt(args, 0), Attempt: argInt(args, 2)})
	case dispOnAutoReconnected:
		c.onEvent(session.Event{Name: "reconnected"})
	}
}

// Hwnd returns the control's window handle.
func (c *Client) Hwnd() windows.HWND { return c.hwnd }

var authLevels = map[string]int32{"none": 0, "required": 1, "warn": 2}

var gatewayUsage = map[string]int32{"never": 0, "always": 1, "detect": 2}

var audioModes = map[string]int32{"local": 0, "remote": 1, "none": 2}

// Connection-speed presets: the control's NetworkConnectionType and the
// PerformanceFlags mstsc pairs with it.
var speeds = map[string]struct{ connType, perfFlags int32 }{
	"modem":          {1, 0x0F},
	"broadband-low":  {2, 0x101},
	"satellite":      {3, 0x101},
	"broadband-high": {4, 0x180},
	"wan":            {5, 0x180},
	"lan":            {6, 0x180},
}

// sub returns the newest available settings sub-object of the control.
func (c *Client) sub(names ...string) *ole.IDispatch {
	for _, name := range names {
		if v, err := oleutil.GetProperty(c.disp, name); err == nil && v.ToIDispatch() != nil {
			return v.ToIDispatch()
		}
	}
	return nil
}

// Apply writes the connection settings to the control. w and h are the
// initial remote desktop size in pixels. Settings the installed control
// doesn't support are skipped and returned as warnings.
func (c *Client) Apply(cfg session.Config, w, h int32) (warnings []string, err error) {
	if cfg.Host == "" {
		return nil, errors.New("host is required")
	}
	if _, err := oleutil.PutProperty(c.disp, "Server", cfg.Host); err != nil {
		return nil, fmt.Errorf("set Server: %w", err)
	}
	put := func(d *ole.IDispatch, name string, value interface{}) {
		if d == nil {
			warnings = append(warnings, name+": not supported by this RDP control")
			return
		}
		if _, err := oleutil.PutProperty(d, name, value); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", name, err))
		}
	}

	if cfg.Username != "" {
		put(c.disp, "UserName", cfg.Username)
	}
	if cfg.Domain != "" {
		put(c.disp, "Domain", cfg.Domain)
	}
	if cfg.Password != "" {
		put(c.adv, "ClearTextPassword", cfg.Password)
	}
	if cfg.Port != 0 {
		put(c.adv, "RDPPort", int32(cfg.Port))
	}

	put(c.disp, "DesktopWidth", w)
	put(c.disp, "DesktopHeight", h)
	if cfg.ColorDepth != 0 {
		put(c.disp, "ColorDepth", int32(cfg.ColorDepth))
	}
	put(c.adv, "SmartSizing", true)
	put(c.adv, "DisplayConnectionBar", true)
	if cfg.Fullscreen {
		put(c.disp, "FullScreenTitle", cfg.Name)
		put(c.disp, "FullScreen", true)
	}

	put(c.adv, "ConnectToAdministerServer", cfg.Console)
	put(c.adv, "EnableAutoReconnect", cfg.AutoReconnect)
	put(c.adv, "EnableCredSspSupport", cfg.NLA)
	if level, ok := authLevels[cfg.AuthLevel]; ok {
		put(c.adv, "AuthenticationLevel", level)
	}

	put(c.adv, "RedirectClipboard", cfg.RedirectClipboard)
	put(c.adv, "RedirectDrives", cfg.RedirectDrives)
	put(c.adv, "RedirectPrinters", cfg.RedirectPrinters)
	if mode, ok := audioModes[cfg.Sound]; ok {
		secured := c.sub("SecuredSettings3", "SecuredSettings2")
		put(secured, "AudioRedirectionMode", mode)
		if secured != nil {
			secured.Release()
		}
	}

	if preset, ok := speeds[cfg.Speed]; ok {
		put(c.adv, "NetworkConnectionType", preset.connType)
		put(c.adv, "PerformanceFlags", preset.perfFlags)
	} else {
		put(c.adv, "NetworkConnectionType", int32(7)) // detect automatically
		put(c.adv, "BandwidthDetection", true)
	}

	if usage := gatewayUsage[cfg.Gateway.Usage]; usage != 0 && cfg.Gateway.Host != "" {
		gw := c.sub("TransportSettings4", "TransportSettings3", "TransportSettings2")
		put(gw, "GatewayProfileUsageMethod", int32(1)) // use these settings, not the system default
		put(gw, "GatewayUsageMethod", usage)
		put(gw, "GatewayHostname", cfg.Gateway.Host)
		put(gw, "GatewayCredsSource", int32(0)) // username and password
		user, domain, password := cfg.Gateway.Username, cfg.Gateway.Domain, cfg.Gateway.Password
		if cfg.Gateway.SameCredentials {
			put(gw, "GatewayCredSharing", int32(1))
			user, domain, password = cfg.Username, cfg.Domain, cfg.Password
		} else {
			put(gw, "GatewayCredSharing", int32(0))
		}
		if user != "" {
			put(gw, "GatewayUsername", user)
			put(gw, "GatewayDomain", domain)
		}
		if password != "" {
			put(gw, "GatewayPassword", password)
		}
		if gw != nil {
			gw.Release()
		}
	}
	return warnings, nil
}

// ToggleFullScreen switches the session between its tab and the full screen.
func (c *Client) ToggleFullScreen() error {
	v, err := oleutil.GetProperty(c.disp, "FullScreen")
	if err != nil {
		return err
	}
	_, err = oleutil.PutProperty(c.disp, "FullScreen", int16(v.Val) == 0)
	return err
}

// Connect starts the connection; progress arrives as events.
func (c *Client) Connect() error {
	_, err := oleutil.CallMethod(c.disp, "Connect")
	return err
}

// Connected reports whether the control is connected or connecting.
func (c *Client) Connected() bool {
	v, err := oleutil.GetProperty(c.disp, "Connected")
	return err == nil && int16(v.Val) != 0
}

// Disconnect ends the session; a "disconnected" event follows.
func (c *Client) Disconnect() error {
	if !c.Connected() {
		return nil
	}
	_, err := oleutil.CallMethod(c.disp, "Disconnect")
	return err
}

// Move resizes the control's window to fill a w x h parent client area.
func (c *Client) Move(w, h int32) {
	procMoveWindow.Call(uintptr(c.hwnd), 0, 0, uintptr(w), uintptr(h), 1)
}

// Focus gives keyboard focus to the control.
func (c *Client) Focus() {
	procSetFocus.Call(uintptr(c.hwnd))
}

// UpdateDisplay asks the server to change the remote desktop to w x h. It is
// a no-op until login has completed.
func (c *Client) UpdateDisplay(w, h int32) error {
	if !c.loggedIn || w <= 0 || h <= 0 {
		return nil
	}
	_, err := oleutil.CallMethod(c.disp, "UpdateSessionDisplaySettings", w, h, w, h, int32(0), int32(100), int32(100))
	return err
}

// Close releases the control and destroys its window.
func (c *Client) Close() {
	if c.point != nil {
		if c.cookie != 0 {
			c.point.Unadvise(c.cookie)
		}
		c.point.Release()
		c.point = nil
	}
	if c.sink != nil {
		c.sink.free()
		c.sink = nil
	}
	if c.adv != nil {
		c.adv.Release()
		c.adv = nil
	}
	if c.disp != nil {
		c.disp.Release()
		c.disp = nil
	}
	if c.hwnd != 0 {
		procDestroyWindow.Call(uintptr(c.hwnd))
		c.hwnd = 0
	}
}
