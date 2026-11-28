package webui

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BOS-Development/esi-bridge/internal/assets"
)

func TestIndexHTMLInjectsSessionToken(t *testing.T) {
	const token = "0123456789abcdef"
	html, err := IndexHTML(token)
	if err != nil {
		t.Fatalf("IndexHTML: %v", err)
	}
	if bytes.Contains(html, []byte(tokenPlaceholder)) {
		t.Fatal("placeholder left in output")
	}
	if !bytes.Contains(html, []byte(`content="`+token+`"`)) {
		t.Fatal("session token not injected")
	}
}

func get(t *testing.T, h http.Handler, path string) *http.Response {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Result()
}

func TestStaticServerServesAssets(t *testing.T) {
	h := StaticServer()
	for _, tc := range []struct{ path, wantType string }{
		{"/app/static/app.js", "javascript"},
		{"/app/static/app.css", "text/css"},
	} {
		resp := get(t, h, tc.path)
		body, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s: status %d", tc.path, resp.StatusCode)
			continue
		}
		if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, tc.wantType) {
			t.Errorf("%s: Content-Type %q, want %s", tc.path, ct, tc.wantType)
		}
		if len(body) == 0 {
			t.Errorf("%s: empty body", tc.path)
		}
	}
}

func TestStaticServerServesIcon(t *testing.T) {
	resp := get(t, StaticServer(), iconPath)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/png" {
		t.Errorf("Content-Type %q, want image/png", ct)
	}
	if !bytes.Equal(body, assets.PNG) {
		t.Error("icon body does not match the embedded PNG")
	}
}

func TestStaticServerMissingFile(t *testing.T) {
	if resp := get(t, StaticServer(), "/app/static/nope.js"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d, want 404", resp.StatusCode)
	}
}
