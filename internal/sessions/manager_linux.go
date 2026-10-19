//go:build linux

package sessions

/*
#cgo pkg-config: gtk+-3.0 x11
#include <string.h>
#include <gtk/gtk.h>
#include <gdk/gdkx.h>
#include <X11/Xlib.h>

// GTK, and GDK's X connection, may only be used on the GTK main thread, so
// everything below runs there by way of g_idle_add.

static GtkWidget *find_webview(GtkWidget *w) {
	if (strcmp(G_OBJECT_TYPE_NAME(w), "WebKitWebView") == 0) {
		return w;
	}
	if (!GTK_IS_CONTAINER(w)) {
		return NULL;
	}
	GList *children = gtk_container_get_children(GTK_CONTAINER(w));
	GtkWidget *found = NULL;
	for (GList *l = children; l != NULL && found == NULL; l = l->next) {
		found = find_webview(l->data);
	}
	g_list_free(children);
	return found;
}

// main_window returns this process's visible top-level window and the web
// view inside it.
static GtkWidget *main_window(GtkWidget **webview) {
	GList *tops = gtk_window_list_toplevels();
	GtkWidget *found = NULL;
	for (GList *l = tops; l != NULL && found == NULL; l = l->next) {
		GtkWidget *w = l->data;
		if (!gtk_widget_get_visible(w) || !gtk_widget_get_realized(w)) {
			continue;
		}
		if ((*webview = find_webview(w)) != NULL) {
			found = w;
		}
	}
	g_list_free(tops);
	return found;
}

enum { found_ok, found_none, found_not_x11 };

typedef struct {
	GMutex mu;
	GCond cond;
	gboolean done;
	int result;
	Window xid;
} find_req;

static gboolean find_cb(gpointer data) {
	find_req *r = data;
	GtkWidget *webview, *top;
	if (!GDK_IS_X11_DISPLAY(gdk_display_get_default())) {
		r->result = found_not_x11;
	} else if ((top = main_window(&webview)) == NULL) {
		r->result = found_none;
	} else {
		r->result = found_ok;
		r->xid = GDK_WINDOW_XID(gtk_widget_get_window(top));
	}
	g_mutex_lock(&r->mu);
	r->done = TRUE;
	g_cond_signal(&r->cond);
	g_mutex_unlock(&r->mu);
	return G_SOURCE_REMOVE;
}

// find_main_window waits for the GTK thread to look up the main window. It
// must not be called on the GTK thread itself.
static int find_main_window(Window *xid) {
	find_req r = {0};
	g_mutex_init(&r.mu);
	g_cond_init(&r.cond);
	g_idle_add(find_cb, &r);
	g_mutex_lock(&r.mu);
	while (!r.done) {
		g_cond_wait(&r.cond, &r.mu);
	}
	g_mutex_unlock(&r.mu);
	g_mutex_clear(&r.mu);
	g_cond_clear(&r.cond);
	*xid = r.xid;
	return r.result;
}

typedef struct {
	Window xid;
	int x, y, w, h;
	gboolean visible;
} place_req;

static gboolean place_cb(gpointer data) {
	place_req *r = data;
	GdkDisplay *display = gdk_display_get_default();
	GtkWidget *webview, *top = main_window(&webview);
	if (top != NULL && GDK_IS_X11_DISPLAY(display)) {
		// The bounds are measured from the web view, but the session window
		// is a child of the whole top-level window, which also holds the
		// menu bar.
		int dx = 0, dy = 0;
		gtk_widget_translate_coordinates(webview, top, 0, 0, &dx, &dy);
		int scale = gtk_widget_get_scale_factor(top);
		Display *x = GDK_DISPLAY_XDISPLAY(display);
		// The session window may already be gone.
		gdk_x11_display_error_trap_push(display);
		XMoveResizeWindow(x, r->xid, r->x + dx * scale, r->y + dy * scale, MAX(r->w, 1), MAX(r->h, 1));
		if (r->visible) {
			XMapRaised(x, r->xid);
		} else {
			XUnmapWindow(x, r->xid);
		}
		gdk_x11_display_error_trap_pop_ignored(display);
	}
	g_free(r);
	return G_SOURCE_REMOVE;
}

static void queue_place(Window xid, int x, int y, int w, int h, gboolean visible) {
	place_req *r = g_new(place_req, 1);
	*r = (place_req){xid, x, y, w, h, visible};
	g_idle_add(place_cb, r);
}
*/
import "C"

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
)

const helperName = "myrdp-session"

// mainWindow returns the X11 window of this process's main window, which the
// session windows are embedded into.
func mainWindow() (uintptr, error) {
	var xid C.Window
	switch C.find_main_window(&xid) {
	case C.found_none:
		return 0, errors.New("main window not found")
	case C.found_not_x11:
		return 0, fmt.Errorf("sessions need X11 or XWayland, but the program is not using X11 (GDK_BACKEND is %q)", os.Getenv("GDK_BACKEND"))
	}
	return uintptr(xid), nil
}

// placeWindow positions a session window above the web view. It is queued on
// the GTK thread, so an unresponsive helper can't block the UI.
func placeWindow(hwnd uintptr, b Bounds, visible bool) {
	var v C.gboolean
	if visible {
		v = 1
	}
	C.queue_place(C.Window(hwnd), C.int(b.X), C.int(b.Y), C.int(b.W), C.int(b.H), v)
}

func hideConsole(cmd *exec.Cmd) {}
