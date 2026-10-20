package freerdp

import (
	"reflect"
	"testing"

	"myrdp/internal/session"
)

func TestParseVersion(t *testing.T) {
	for _, tc := range []struct {
		out     string
		version string
		major   int
		ok      bool
	}{
		{"This is FreeRDP version 3.32.2 (97eff6ddfb)\n", "3.32.2", 3, true},
		{"This is FreeRDP version 2.11.7 (2.11.7)\n", "2.11.7", 2, true},
		{"command not found\n", "", 0, false},
	} {
		version, major, ok := parseVersion(tc.out)
		if version != tc.version || major != tc.major || ok != tc.ok {
			t.Errorf("parseVersion(%q) = %q, %d, %v; want %q, %d, %v", tc.out, version, major, ok, tc.version, tc.major, tc.ok)
		}
	}
}

func TestArgs(t *testing.T) {
	// The schema's defaults, for a connection that sets only its host.
	defaults := session.Config{
		Name: "srv", Host: "srv.example", Port: 3389,
		AutoResize: true, ColorDepth: 32, AutoReconnect: true, NLA: true, AuthLevel: "warn",
		Gateway:           session.Gateway{Usage: "never", SameCredentials: true},
		RedirectClipboard: true, Sound: "local", Speed: "auto",
	}

	for _, tc := range []struct {
		name string
		edit func(*session.Config)
		want []string
	}{
		{"defaults", func(*session.Config) {}, []string{
			"/v:srv.example:3389", "/t:srv", "/p:", "/parent-window:42", "/w:1024", "/h:768", "+dynamic-resolution",
			"/bpp:32", "+auto-reconnect", "/cert:tofu", "+clipboard", "/sound", "/network:auto",
		}},
		{"everything else", func(c *session.Config) {
			c.Username, c.Domain, c.Password = "me", "CORP", "pw"
			c.Width, c.Height = 1280, 720
			c.ColorDepth = 16
			c.Console, c.AutoReconnect, c.NLA = true, false, false
			c.AuthLevel = "none"
			c.Gateway = session.Gateway{Usage: "always", Host: "gw.example", Username: "gwme", Domain: "GW", Password: "gwpw"}
			c.RedirectClipboard, c.RedirectDrives, c.RedirectPrinters = false, true, true
			c.Sound, c.Speed = "remote", "satellite"
		}, []string{
			"/v:srv.example:3389", "/t:srv", "/u:me", "/d:CORP", "/p:pw",
			"/parent-window:42", "/w:1280", "/h:720", "/bpp:16", "/admin", "/sec:nla:off", "/cert:ignore",
			"/gateway:g:gw.example,usage-method:direct,u:gwme,d:GW,p:gwpw",
			"-clipboard", "+drives", "/printer", "/audio-mode:1", "/network:3",
		}},
		{"full screen, gateway with the same credentials", func(c *session.Config) {
			c.Username, c.Password = "me", "pw"
			c.Fullscreen = true
			c.AuthLevel = "required"
			c.Gateway = session.Gateway{Usage: "detect", Host: "gw.example", SameCredentials: true}
			c.Sound = "none"
		}, []string{
			"/v:srv.example:3389", "/t:srv", "/u:me", "/p:pw", "/f", "/bpp:32", "+auto-reconnect",
			"/cert:deny", "/gateway:g:gw.example,usage-method:detect,u:me,p:pw",
			"+clipboard", "/audio-mode:2", "/network:auto",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := defaults
			tc.edit(&cfg)
			if got := Args(cfg, 42, 1024, 768); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Args() =\n  %q\nwant\n  %q", got, tc.want)
			}
		})
	}
}
