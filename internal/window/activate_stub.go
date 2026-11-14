//go:build !(darwin && cgo)

package window

// makeForeground is a no-op off macOS: Windows (WebView2) and Linux (GTK)
// windows already appear as ordinary foreground windows, and the macOS
// LSUIElement/activation-policy problem it solves doesn't arise there.
func makeForeground() {}
