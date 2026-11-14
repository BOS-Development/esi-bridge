//go:build cgo

package window

import (
	"runtime"

	webview "github.com/webview/webview_go"
)

const supported = true

// Open creates the native window, navigates it to url, and blocks until the
// user closes it. It must be called from the process's main goroutine before
// any other goroutine has had a chance to migrate it off the initial OS
// thread: the underlying webview drives a native UI run loop that, on macOS,
// only functions on thread 0. Open locks the calling goroutine to its current
// thread to hold it there for the window's lifetime.
//
// This is why the window runs in its own process (see the package doc): the
// single main-thread run loop can't be shared with the system tray's.
func Open(title, url string) error {
	runtime.LockOSThread()

	// debug=false: no devtools / right-click inspector — this is a shipped
	// app window, not a browser.
	w := webview.New(false)
	defer w.Destroy()

	// Creating the webview sets up the shared native application; promote
	// this process to a foreground app now (a no-op off macOS). See
	// makeForeground.
	makeForeground()

	w.SetTitle(title)
	// A sensible default first size; the window stays user-resizable.
	w.SetSize(1024, 720, webview.HintNone)
	w.Navigate(url)
	w.Run() // blocks until the window is closed
	return nil
}
