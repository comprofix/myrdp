package model

import (
	"fmt"
	"strconv"
)

type FieldType string

const (
	String FieldType = "string"
	Int    FieldType = "int"
	Bool   FieldType = "bool"
	Enum   FieldType = "enum"
	// Secret fields hold a password. The file stores only `true` to mark
	// that one is saved; the value itself lives in the OS keyring.
	Secret FieldType = "secret"
)

type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field describes one setting. The settings UI is generated from this list.
type Field struct {
	Key     string    `json:"key"`
	Label   string    `json:"label"`
	Group   string    `json:"group"`
	Type    FieldType `json:"type"`
	Options []Option  `json:"options,omitempty"`
	Default any       `json:"default"`
	// ConnectionOnly fields are not inherited and only apply to connections.
	ConnectionOnly bool   `json:"connectionOnly,omitempty"`
	Help           string `json:"help,omitempty"`
}

// Setting keys used outside the schema table.
const (
	KeyHost            = "host"
	KeyUsername        = "username"
	KeyDomain          = "domain"
	KeyPassword        = "passwordStored"
	KeyGatewayPassword = "rdp.gateway.passwordStored"
)

func opts(pairs ...string) []Option {
	out := make([]Option, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Option{Value: pairs[i], Label: pairs[i+1]})
	}
	return out
}

func resolutions() []Option {
	out := opts("fit", "Fit to window", "fullscreen", "Full screen")
	for _, r := range []string{"1024x768", "1280x720", "1280x1024", "1366x768", "1440x900", "1600x900", "1680x1050", "1920x1080", "1920x1200", "2560x1440", "3840x2160"} {
		out = append(out, Option{Value: r, Label: r})
	}
	return out
}

// Fields is the first-version setting list, in display order.
var Fields = []Field{
	{Key: KeyHost, Label: "Host", Group: "Connection", Type: String, Default: "", ConnectionOnly: true, Help: "Server name or IP address"},
	{Key: "description", Label: "Description", Group: "Connection", Type: String, Default: ""},
	{Key: "rdp.port", Label: "Port", Group: "Connection", Type: Int, Default: 3389},

	{Key: KeyUsername, Label: "Username", Group: "Credentials", Type: String, Default: ""},
	{Key: KeyDomain, Label: "Domain", Group: "Credentials", Type: String, Default: ""},
	{Key: KeyPassword, Label: "Password", Group: "Credentials", Type: Secret, Default: false},

	{Key: "rdp.resolution", Label: "Resolution", Group: "Display", Type: Enum, Options: resolutions(), Default: "fit"},
	{Key: "rdp.autoResize", Label: "Resize session with window", Group: "Display", Type: Bool, Default: true, Help: "Applies to \"Fit to window\""},
	{Key: "rdp.colorDepth", Label: "Colour depth", Group: "Display", Type: Enum, Default: "32",
		Options: opts("15", "High colour (15 bit)", "16", "High colour (16 bit)", "24", "True colour (24 bit)", "32", "Highest quality (32 bit)")},

	{Key: "rdp.console", Label: "Connect to console/admin session", Group: "Session", Type: Bool, Default: false},
	{Key: "rdp.autoReconnect", Label: "Reconnect if the connection drops", Group: "Session", Type: Bool, Default: true},

	{Key: "rdp.nla", Label: "Network Level Authentication (CredSSP)", Group: "Security", Type: Bool, Default: true},
	{Key: "rdp.authLevel", Label: "If server authentication fails", Group: "Security", Type: Enum, Default: "warn",
		Options: opts("none", "Connect and don't warn me", "warn", "Warn me", "required", "Do not connect")},

	{Key: "rdp.gateway.usage", Label: "Use RD Gateway", Group: "RD Gateway", Type: Enum, Default: "never",
		Options: opts("never", "Never", "always", "Always", "detect", "Bypass for local addresses")},
	{Key: "rdp.gateway.host", Label: "Gateway host", Group: "RD Gateway", Type: String, Default: ""},
	{Key: "rdp.gateway.credentials", Label: "Gateway credentials", Group: "RD Gateway", Type: Enum, Default: "same",
		Options: opts("same", "Same as connection", "separate", "Separate credentials")},
	{Key: "rdp.gateway.username", Label: "Gateway username", Group: "RD Gateway", Type: String, Default: ""},
	{Key: "rdp.gateway.domain", Label: "Gateway domain", Group: "RD Gateway", Type: String, Default: ""},
	{Key: KeyGatewayPassword, Label: "Gateway password", Group: "RD Gateway", Type: Secret, Default: false},

	{Key: "rdp.redirect.clipboard", Label: "Clipboard", Group: "Redirection", Type: Bool, Default: true},
	{Key: "rdp.redirect.drives", Label: "Drives", Group: "Redirection", Type: Bool, Default: false},
	{Key: "rdp.redirect.printers", Label: "Printers", Group: "Redirection", Type: Bool, Default: false},
	{Key: "rdp.redirect.sound", Label: "Sound", Group: "Redirection", Type: Enum, Default: "local",
		Options: opts("local", "Play on this computer", "remote", "Leave on remote computer", "none", "Do not play")},

	{Key: "rdp.speed", Label: "Connection speed", Group: "Performance", Type: Enum, Default: "auto",
		Options: opts("auto", "Detect automatically", "modem", "Modem (56 kbps)", "broadband-low", "Low-speed broadband",
			"satellite", "Satellite", "broadband-high", "High-speed broadband", "wan", "WAN", "lan", "LAN (10 Mbps or higher)")},
}

var fieldByKey = func() map[string]*Field {
	m := map[string]*Field{}
	for i := range Fields {
		m[Fields[i].Key] = &Fields[i]
	}
	return m
}()

// FieldFor returns the schema entry for key, or nil.
func FieldFor(key string) *Field { return fieldByKey[key] }

// Coerce converts v to the field's Go type: string, int or bool. Enum values
// must be one of the field's options.
func (f *Field) Coerce(v any) (any, error) {
	bad := func() (any, error) {
		return nil, fmt.Errorf("%s: invalid value %v", f.Label, v)
	}
	switch f.Type {
	case String:
		if s, ok := v.(string); ok {
			return s, nil
		}
	case Enum:
		if s, ok := v.(string); ok {
			for _, o := range f.Options {
				if o.Value == s {
					return s, nil
				}
			}
		}
	case Bool, Secret:
		if b, ok := v.(bool); ok {
			return b, nil
		}
	case Int:
		switch n := v.(type) {
		case int:
			return n, nil
		case float64:
			if n == float64(int(n)) {
				return int(n), nil
			}
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i, nil
			}
		}
	}
	return bad()
}

// Resolve returns the effective value of the field for n and the node that
// supplied it. The source is nil when the built-in default applies. Values
// in the file that don't fit the field are skipped rather than trusted.
func (f *Field) Resolve(n *Node) (any, *Node) {
	if f.ConnectionOnly {
		if v, ok := n.Settings[f.Key]; ok {
			if c, err := f.Coerce(v); err == nil {
				return c, n
			}
		}
		return f.Default, nil
	}
	for p := n; p != nil; p = p.Parent {
		if v, ok := p.Settings[f.Key]; ok {
			if c, err := f.Coerce(v); err == nil {
				return c, p
			}
		}
	}
	return f.Default, nil
}

// ResolveAll returns the effective value of every schema field for n.
func ResolveAll(n *Node) map[string]any {
	out := make(map[string]any, len(Fields))
	for i := range Fields {
		out[Fields[i].Key], _ = Fields[i].Resolve(n)
	}
	return out
}
