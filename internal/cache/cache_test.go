package cache

import (
	"net/http"
	"testing"
	"time"
)

func TestGetSetExpiry(t *testing.T) {
	c := New()
	key := Key(123, "GET", "/characters/123/", "")

	c.Set(key, Entry{StatusCode: 200, Body: []byte("fresh"), ExpiresAt: time.Now().Add(time.Hour)})
	e, ok := c.Get(key)
	if !ok {
		t.Fatal("expected cache hit for unexpired entry")
	}
	if string(e.Body) != "fresh" {
		t.Fatalf("got body %q", e.Body)
	}

	staleKey := Key(456, "GET", "/characters/456/", "")
	c.Set(staleKey, Entry{StatusCode: 200, Body: []byte("stale"), ExpiresAt: time.Now().Add(-time.Hour)})
	if _, ok := c.Get(staleKey); ok {
		t.Fatal("expected cache miss: Set should refuse to store an already-expired entry")
	}
}

func TestSweepRemovesExpired(t *testing.T) {
	c := New()
	key := Key(1, "GET", "/x", "")
	c.entries[key] = Entry{ExpiresAt: time.Now().Add(-time.Minute)} // bypass Set's guard directly

	c.Sweep()

	c.mu.Lock()
	_, ok := c.entries[key]
	c.mu.Unlock()
	if ok {
		t.Fatal("expected Sweep to remove expired entry")
	}
}

func TestParseExpires(t *testing.T) {
	h := http.Header{}
	future := time.Now().Add(time.Minute).UTC().Format(http.TimeFormat)
	h.Set("Expires", future)

	got := ParseExpires(h)
	if got.IsZero() {
		t.Fatal("expected a parsed time")
	}

	h.Set("Expires", "not-a-date")
	if !ParseExpires(h).IsZero() {
		t.Fatal("expected zero time for unparsable Expires header")
	}
}
