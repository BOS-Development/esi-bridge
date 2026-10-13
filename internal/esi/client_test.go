package esi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestForwardSendsAuthPathAndQuery(t *testing.T) {
	var gotAuth, gotPath, gotQuery, gotUA, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotUA = r.Header.Get("User-Agent")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, "ok")
	}))
	defer srv.Close()

	c := NewClient(srv.URL)
	resp, err := c.Forward(context.Background(), "tok-123", http.MethodPost,
		"/characters/42/assets/", "page=2", strings.NewReader("payload"))
	if err != nil {
		t.Fatalf("Forward: %v", err)
	}
	defer resp.Body.Close()

	if gotAuth != "Bearer tok-123" {
		t.Errorf("Authorization = %q, want Bearer tok-123", gotAuth)
	}
	if gotPath != "/characters/42/assets/" {
		t.Errorf("path = %q", gotPath)
	}
	if gotQuery != "page=2" {
		t.Errorf("query = %q", gotQuery)
	}
	if gotBody != "payload" {
		t.Errorf("body = %q", gotBody)
	}
	if !strings.Contains(gotUA, "esi-proxy") {
		t.Errorf("User-Agent = %q, want it to identify esi-proxy", gotUA)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestForwardBlockedByErrorLimiter(t *testing.T) {
	c := NewClient("http://unused.invalid")
	// Drive the error budget below threshold with a reset window still open.
	h := http.Header{}
	h.Set("X-Esi-Error-Limit-Remain", "1")
	h.Set("X-Esi-Error-Limit-Reset", "60")
	c.limiter.Observe(h)

	_, err := c.Forward(context.Background(), "tok", http.MethodGet, "/x", "", nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("Forward err = %v, want ErrRateLimited", err)
	}
}

func TestForwardBadMethod(t *testing.T) {
	c := NewClient("http://unused.invalid")
	if _, err := c.Forward(context.Background(), "tok", "BAD METHOD", "/x", "", nil); err == nil {
		t.Fatal("expected an error building a request with an invalid method")
	}
}

func TestErrorLimiterDefaultsNotBlocked(t *testing.T) {
	l := NewErrorLimiter()
	if l.remain != 100 || l.threshold != 5 {
		t.Fatalf("defaults = remain %d threshold %d, want 100/5", l.remain, l.threshold)
	}
	if wait, blocked := l.Blocked(); blocked || wait != 0 {
		t.Fatalf("fresh limiter Blocked() = (%v, %v), want (0, false)", wait, blocked)
	}
}

func TestErrorLimiterBlockingTransitions(t *testing.T) {
	l := NewErrorLimiter()

	// Budget low, reset window open → blocked with a positive wait.
	h := http.Header{}
	h.Set("X-Esi-Error-Limit-Remain", "2")
	h.Set("X-Esi-Error-Limit-Reset", "30")
	l.Observe(h)
	if wait, blocked := l.Blocked(); !blocked || wait <= 0 {
		t.Fatalf("Blocked() = (%v, %v), want blocked with positive wait", wait, blocked)
	}

	// Budget low but reset window already elapsed (0s) → not blocked.
	h.Set("X-Esi-Error-Limit-Reset", "0")
	l.Observe(h)
	if _, blocked := l.Blocked(); blocked {
		t.Fatal("Blocked() = true after reset window elapsed, want false")
	}

	// Budget recovered above threshold → not blocked.
	h.Set("X-Esi-Error-Limit-Remain", "90")
	h.Set("X-Esi-Error-Limit-Reset", "60")
	l.Observe(h)
	if _, blocked := l.Blocked(); blocked {
		t.Fatal("Blocked() = true with budget restored, want false")
	}
}
