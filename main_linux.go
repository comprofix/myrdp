package main

import "os"

// Sessions are embedded as X11 child windows, so on Wayland the program runs
// through XWayland. A GDK_BACKEND the user has set is left alone.
func init() {
	if os.Getenv("GDK_BACKEND") == "" {
		os.Setenv("GDK_BACKEND", "x11")
	}
}
