// Package freerdp finds the FreeRDP 3 program and turns a session.Config into
// its command-line options. The Linux session helper runs FreeRDP with those
// options.
package freerdp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"time"

	"myrdp/internal/session"
)

// Programs are the names FreeRDP 3's X11 client is installed under:
// xfreerdp3 where distributions version the binaries, xfreerdp upstream.
var Programs = []string{"xfreerdp3", "xfreerdp"}

// Find returns the FreeRDP 3 program to run: MYRDP_FREERDP if it is set,
// otherwise the first of Programs on PATH. The errors are written to be shown
// to the user as they are.
func Find() (string, error) {
	if p := os.Getenv("MYRDP_FREERDP"); p != "" {
		if err := checkVersion(p); err != nil {
			return "", fmt.Errorf("MYRDP_FREERDP is set to %s, but %w", p, err)
		}
		return p, nil
	}
	for _, name := range Programs {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if err := checkVersion(p); err != nil {
			return "", fmt.Errorf("%s was found, but %w", p, err)
		}
		return p, nil
	}
	return "", errors.New("FreeRDP 3 is not installed (looked for xfreerdp3 and xfreerdp). " +
		"Install it with your package manager: freerdp on Arch, freerdp3-x11 on Debian and Ubuntu.")
}

func checkVersion(path string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "/version").Output()
	if err != nil {
		return fmt.Errorf("it could not be run: %w", err)
	}
	version, major, ok := parseVersion(string(out))
	switch {
	case !ok:
		return errors.New("it did not report a FreeRDP version")
	case major != 3:
		return fmt.Errorf("it is FreeRDP %s and version 3 is required", version)
	}
	return nil
}

var versionRE = regexp.MustCompile(`FreeRDP version ((\d+)\.[0-9.]+)`)

// parseVersion reads the output of "xfreerdp /version", for example "This is
// FreeRDP version 3.32.2 (97eff6ddfb)".
func parseVersion(out string) (version string, major int, ok bool) {
	m := versionRE.FindStringSubmatch(out)
	if m == nil {
		return "", 0, false
	}
	major, _ = strconv.Atoi(m[2])
	return m[1], major, true
}

// speeds maps the connection-speed presets to FreeRDP's /network values.
// FreeRDP has no name for satellite, so its protocol value is passed.
var speeds = map[string]string{
	"auto":           "auto",
	"modem":          "modem",
	"broadband-low":  "broadband-low",
	"satellite":      "3",
	"broadband-high": "broadband-high",
	"wan":            "wan",
	"lan":            "lan",
}

// Args returns the FreeRDP options for cfg, one per element, as read by
// /args-from:stdin. The session is embedded into the X11 window parent at
// w by h pixels, unless cfg asks for full screen.
func Args(cfg session.Config, parent uint64, w, h int) []string {
	host := cfg.Host
	if cfg.Port != 0 {
		host += ":" + strconv.Itoa(cfg.Port)
	}
	args := []string{"/v:" + host}
	if cfg.Name != "" {
		args = append(args, "/t:"+cfg.Name)
	}
	if cfg.Username != "" {
		args = append(args, "/u:"+cfg.Username)
	}
	if cfg.Domain != "" {
		args = append(args, "/d:"+cfg.Domain)
	}
	// Without a password FreeRDP asks for one on a terminal nobody can see
	// and never connects, so an empty one is passed instead.
	args = append(args, "/p:"+cfg.Password)

	switch {
	case cfg.Fullscreen:
		args = append(args, "/f")
	case cfg.Width > 0 && cfg.Height > 0:
		args = append(args, "/parent-window:"+strconv.FormatUint(parent, 10),
			"/w:"+strconv.Itoa(cfg.Width), "/h:"+strconv.Itoa(cfg.Height))
	default:
		args = append(args, "/parent-window:"+strconv.FormatUint(parent, 10))
		if w > 0 && h > 0 {
			args = append(args, "/w:"+strconv.Itoa(w), "/h:"+strconv.Itoa(h))
		}
		if cfg.AutoResize {
			args = append(args, "+dynamic-resolution")
		}
	}
	if cfg.ColorDepth > 0 {
		args = append(args, "/bpp:"+strconv.Itoa(cfg.ColorDepth))
	}

	if cfg.Console {
		args = append(args, "/admin")
	}
	if cfg.AutoReconnect {
		args = append(args, "+auto-reconnect")
	}

	if !cfg.NLA {
		args = append(args, "/sec:nla:off")
	}
	// FreeRDP can't ask about a certificate without a terminal, so "warn"
	// trusts a server on first use and refuses it if its certificate changes.
	switch cfg.AuthLevel {
	case "none":
		args = append(args, "/cert:ignore")
	case "warn":
		args = append(args, "/cert:tofu")
	case "required":
		args = append(args, "/cert:deny")
	}

	if g := cfg.Gateway; g.Usage == "always" || g.Usage == "detect" {
		method := "direct"
		if g.Usage == "detect" {
			method = "detect"
		}
		user, domain, password := g.Username, g.Domain, g.Password
		if g.SameCredentials {
			user, domain, password = cfg.Username, cfg.Domain, cfg.Password
		}
		// The gateway settings share one comma-separated option, so a comma in
		// any of them cuts it short.
		opt := "/gateway:g:" + g.Host + ",usage-method:" + method
		if user != "" {
			opt += ",u:" + user
		}
		if domain != "" {
			opt += ",d:" + domain
		}
		opt += ",p:" + password
		args = append(args, opt)
	}

	if cfg.RedirectClipboard {
		args = append(args, "+clipboard")
	} else {
		args = append(args, "-clipboard")
	}
	if cfg.RedirectDrives {
		args = append(args, "+drives")
	}
	if cfg.RedirectPrinters {
		args = append(args, "/printer")
	}
	switch cfg.Sound {
	case "local":
		args = append(args, "/sound")
	case "remote":
		args = append(args, "/audio-mode:1")
	case "none":
		args = append(args, "/audio-mode:2")
	}

	if v, ok := speeds[cfg.Speed]; ok {
		args = append(args, "/network:"+v)
	}
	return args
}
