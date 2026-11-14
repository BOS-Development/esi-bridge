//go:build !cgo

package window

import "errors"

const supported = false

// Open is unavailable in a CGO-disabled build: the native webview is a C
// library. Callers check Supported first and fall back to a browser; this
// definition exists only so such a build still compiles.
func Open(title, url string) error {
	return errors.New("window: native window unavailable in this build (built without CGO)")
}
