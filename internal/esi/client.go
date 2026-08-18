package esi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// Client forwards requests to the ESI API on behalf of a character.
type Client struct {
	baseURL    string
	httpClient *http.Client

	limiter *ErrorLimiter
}

func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		limiter:    NewErrorLimiter(),
	}
}

// Forward issues method/path (path is relative to the ESI base URL, e.g.
// "/characters/12345/assets/") with accessToken as bearer auth, copying
// query params and body through unchanged. It returns the raw *http.Response
// so the caller (the proxy handler) can inspect headers for caching and
// stream the body back to the client.
func (c *Client) Forward(ctx context.Context, accessToken, method, path, rawQuery string, body io.Reader) (*http.Response, error) {
	if wait, blocked := c.limiter.Blocked(); blocked {
		return nil, fmt.Errorf("esi error limit exhausted, retry after %s: %w", wait, ErrRateLimited)
	}

	url := c.baseURL + path
	if rawQuery != "" {
		url += "?" + rawQuery
	}

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("build esi request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", "esi-proxy (github.com/BOS-Development/esi-proxy)")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("esi request: %w", err)
	}

	c.limiter.Observe(resp.Header)
	return resp, nil
}

// ErrorLimiter tracks ESI's per-app error budget (X-Esi-Error-Limit-Remain /
// X-Esi-Error-Limit-Reset) and blocks outgoing requests once it's nearly
// exhausted, so this proxy doesn't get the shared client banned by CCP.
type ErrorLimiter struct {
	remain    int
	resetAt   time.Time
	threshold int
}

func NewErrorLimiter() *ErrorLimiter {
	return &ErrorLimiter{remain: 100, threshold: 5}
}

func (l *ErrorLimiter) Observe(h http.Header) {
	if v := h.Get("X-Esi-Error-Limit-Remain"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			l.remain = n
		}
	}
	if v := h.Get("X-Esi-Error-Limit-Reset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			l.resetAt = time.Now().Add(time.Duration(n) * time.Second)
		}
	}
}

// Blocked reports whether the remaining error budget is below the safety
// threshold and, if so, how long until the ESI-reported reset window ends.
func (l *ErrorLimiter) Blocked() (wait time.Duration, blocked bool) {
	if l.remain > l.threshold {
		return 0, false
	}
	remaining := time.Until(l.resetAt)
	if remaining <= 0 {
		return 0, false
	}
	return remaining, true
}

var ErrRateLimited = fmt.Errorf("esi error limit exhausted")
