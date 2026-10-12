// Package session defines the platform-neutral contract between the manager
// and a session backend: the resolved settings a backend connects with, and
// the events it reports back.
package session

// Gateway holds RD Gateway settings. Usage is "never", "always" or "detect".
type Gateway struct {
	Usage           string `json:"usage"`
	Host            string `json:"host"`
	SameCredentials bool   `json:"sameCredentials"`
	Username        string `json:"username,omitempty"`
	Domain          string `json:"domain,omitempty"`
	Password        string `json:"password,omitempty"`
}

// Config is a fully resolved connection: nothing is inherited at this point.
type Config struct {
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username,omitempty"`
	Domain   string `json:"domain,omitempty"`
	Password string `json:"password,omitempty"`

	// Width and Height are a fixed remote desktop size; zero means use the
	// size of the hosting window.
	Width      int  `json:"width,omitempty"`
	Height     int  `json:"height,omitempty"`
	Fullscreen bool `json:"fullscreen,omitempty"`
	AutoResize bool `json:"autoResize"`
	ColorDepth int  `json:"colorDepth"`

	Console       bool `json:"console"`
	AutoReconnect bool `json:"autoReconnect"`

	NLA bool `json:"nla"`
	// AuthLevel is "none", "warn" or "required".
	AuthLevel string `json:"authLevel"`

	Gateway Gateway `json:"gateway"`

	RedirectClipboard bool `json:"redirectClipboard"`
	RedirectDrives    bool `json:"redirectDrives"`
	RedirectPrinters  bool `json:"redirectPrinters"`
	// Sound is "local", "remote" or "none".
	Sound string `json:"sound"`

	// Speed is a connection-speed preset: "auto", "modem", "broadband-low",
	// "satellite", "broadband-high", "wan" or "lan".
	Speed string `json:"speed"`
}

// Event is a notification from a session backend, written as one JSON line.
type Event struct {
	Name           string `json:"event"`
	Reason         int32  `json:"reason,omitempty"`
	ExtendedReason int32  `json:"extendedReason,omitempty"`
	Message        string `json:"message,omitempty"`
	Width          int32  `json:"width,omitempty"`
	Height         int32  `json:"height,omitempty"`
	Attempt        int32  `json:"attempt,omitempty"`
	Hwnd           uint64 `json:"hwnd,omitempty"`
}

// Command is an instruction to a session backend, read as one JSON line.
// Cmd is "disconnect", "close", "fullscreen" or "focus".
type Command struct {
	Cmd string `json:"cmd"`
}
