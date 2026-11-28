package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestForwardCallbackReplaysQueryAgainstRunningInstance(t *testing.T) {
	var gotQuery string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sso/callback" {
			t.Errorf("path = %q, want /sso/callback", r.URL.Path)
		}
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	tsURL, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	port, err := strconv.Atoi(tsURL.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}

	rawURL := "eveauth-proxy://callback?code=abc123&state=xyz789"
	if err := forwardCallback(port, rawURL); err != nil {
		t.Fatalf("forwardCallback: %v", err)
	}

	if gotQuery != "code=abc123&state=xyz789" {
		t.Fatalf("query forwarded = %q, want %q", gotQuery, "code=abc123&state=xyz789")
	}
}

func TestForwardCallbackFailsCleanlyWhenNothingListening(t *testing.T) {
	// Port 1 is a privileged/reserved port nothing will ever be listening
	// on in a test environment, so this should reliably fail to connect.
	if err := forwardCallback(1, "eveauth-proxy://callback?code=abc&state=xyz"); err == nil {
		t.Fatal("expected an error when no instance is listening")
	}
}
