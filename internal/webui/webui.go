// Package webui holds the embedded assets for the proxy's full management UI
// (the app window, or a browser in serve-ui dev mode). The server package
// serves these behind its loopback + session-token guards.
package webui

import (
	"bytes"
	"embed"
	"io/fs"
	"net/http"
	"time"

	"github.com/BOS-Development/esi-bridge/internal/assets"
)

//go:embed assets
var uiAssets embed.FS

// tokenPlaceholder is replaced in index.html with the per-process session
// token at serve time, so same-origin UI code can authenticate /manage calls.
const tokenPlaceholder = "__SESSION_TOKEN__"

// IndexHTML returns the management page with the session token injected. The
// token is 32 bytes of crypto/rand, hex-encoded, so it needs no escaping.
func IndexHTML(sessionToken string) ([]byte, error) {
	raw, err := uiAssets.ReadFile("assets/index.html")
	if err != nil {
		return nil, err
	}
	return bytes.Replace(raw, []byte(tokenPlaceholder), []byte(sessionToken), 1), nil
}

// iconPath is where the app icon (internal/assets) is served, for the
// favicon and the header logo.
const iconPath = "/app/static/bridge.png"

// StaticServer serves the UI's static JS/CSS, mounted at /app/static/. The
// "/app" prefix is stripped so a request for /app/static/app.js maps onto
// assets/static/app.js in the embedded tree.
func StaticServer() http.Handler {
	sub, err := fs.Sub(uiAssets, "assets")
	if err != nil {
		// uiAssets is a compile-time embed; "assets" always exists.
		panic("webui: sub FS: " + err.Error())
	}
	files := http.StripPrefix("/app", http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == iconPath {
			http.ServeContent(w, r, "bridge.png", time.Time{}, bytes.NewReader(assets.PNG))
			return
		}
		files.ServeHTTP(w, r)
	})
}
