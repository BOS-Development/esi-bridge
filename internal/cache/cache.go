// Package cache is an in-memory HTTP response cache keyed by character and
// request, honoring the Expires header ESI returns on every response.
package cache

import (
	"fmt"
	"net/http"
	"sync"
	"time"
)

type Entry struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	ExpiresAt  time.Time
}

type Cache struct {
	mu      sync.Mutex
	entries map[string]Entry
}

func New() *Cache {
	return &Cache{entries: make(map[string]Entry)}
}

// Key identifies a cacheable request. ESI responses are character-specific
// even for the same path (private data), so the character ID is part of the
// key.
func Key(characterID int64, method, path, rawQuery string) string {
	return fmt.Sprintf("%d %s %s?%s", characterID, method, path, rawQuery)
}

func (c *Cache) Get(key string) (Entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e, ok := c.entries[key]
	if !ok {
		return Entry{}, false
	}
	if time.Now().After(e.ExpiresAt) {
		delete(c.entries, key)
		return Entry{}, false
	}
	return e, true
}

func (c *Cache) Set(key string, e Entry) {
	if !e.ExpiresAt.After(time.Now()) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = e
}

// Sweep removes expired entries. Call periodically from a background ticker
// to bound memory use for a long-running proxy.
func (c *Cache) Sweep() {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	for k, e := range c.entries {
		if now.After(e.ExpiresAt) {
			delete(c.entries, k)
		}
	}
}

// ParseExpires parses the HTTP Expires header ESI sets on every response. If
// missing or unparsable, it returns the zero time (treated as already
// expired, i.e. not cached).
func ParseExpires(h http.Header) time.Time {
	v := h.Get("Expires")
	if v == "" {
		return time.Time{}
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return time.Time{}
	}
	return t
}
