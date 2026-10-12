package main

import (
	"embed"
	"fmt"
	"os"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

// configDir returns the directory holding connections.json and
// settings.json when one is given with --config <dir> or MYRDP_CONFIG, and
// "" for the per-user default. The arguments are scanned by hand because
// Wails adds flags of its own in dev mode.
func configDir() string {
	args := os.Args[1:]
	for i, arg := range args {
		name, value, hasValue := strings.Cut(strings.TrimLeft(arg, "-"), "=")
		if !strings.HasPrefix(arg, "-") || name != "config" {
			continue
		}
		if hasValue {
			return value
		}
		if i+1 < len(args) {
			return args[i+1]
		}
	}
	return os.Getenv("MYRDP_CONFIG")
}

func main() {
	app := NewApp(configDir())

	err := wails.Run(&options.App{
		Title:     "myrdp",
		Width:     1400,
		Height:    900,
		MinWidth:  800,
		MinHeight: 500,
		Menu:      app.menu(),
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		OnStartup:     app.startup,
		OnShutdown:    app.shutdown,
		OnBeforeClose: app.beforeClose,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			// Follow the desktop's light or dark mode, including live changes.
			Theme: windows.SystemDefault,
		},
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
