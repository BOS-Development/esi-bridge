package browser

import "testing"

// Only the rejection path is tested: a valid URL would launch a real browser.
func TestOpenRejectsNonHTTPURLs(t *testing.T) {
	for _, u := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"eveauth-proxy://callback",
		"C:\\Windows\\System32\\calc.exe",
		"ftp://example.com",
		"",
		"://bad",
	} {
		if err := Open(u); err == nil {
			t.Errorf("Open(%q) succeeded, want refusal", u)
		}
	}
}
