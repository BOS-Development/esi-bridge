package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client is a thin wrapper around the proxy's HTTP API, holding the base URL
// and the api_key this app was issued at registration. It deliberately uses
// nothing but the standard library and the proxy's public HTTP surface — an
// external application couldn't import the proxy's internal packages, so this
// example doesn't either.
type Client struct {
	BaseURL string // e.g. http://localhost:8085, no trailing slash
	APIKey  string // X-Proxy-Api-Key; empty until Register is called
	HTTP    *http.Client
}

func newClient(baseURL string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		HTTP:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Healthz reports whether the proxy is up and answering on BaseURL. It's how
// the app "detects if the proxy is running" before doing anything else.
func (c *Client) Healthz() error {
	resp, err := c.HTTP.Get(c.BaseURL + "/healthz")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d from /healthz", resp.StatusCode)
	}
	return nil
}

// Register self-registers this app with the proxy under name and stores the
// returned api_key on the client. The key carries no access until the user
// approves permission/access requests, so it's safe to keep and reuse.
func (c *Client) Register(name string) error {
	var out struct {
		ApplicationID int64  `json:"application_id"`
		APIKey        string `json:"api_key"`
	}
	if err := c.doJSON(http.MethodPost, "/api/register", map[string]string{"name": name}, &out); err != nil {
		return err
	}
	if out.APIKey == "" {
		return fmt.Errorf("register: proxy returned an empty api_key")
	}
	c.APIKey = out.APIKey
	return nil
}

// RequestPermissions asks for app-wide permissions (e.g. list_characters) and
// returns each one's status: "granted" or "pending". Re-calling it is how the
// app polls for approval — an already-approved permission comes back
// "granted" immediately.
func (c *Client) RequestPermissions(perms ...string) (map[string]string, error) {
	var out map[string]string
	if err := c.doJSON(http.MethodPost, "/api/request-permissions", map[string][]string{"permissions": perms}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// Character is one entry from GET /api/characters.
type Character struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// ListCharacters returns the characters visible to this app. With the
// list_characters permission granted that's every character on the proxy.
func (c *Client) ListCharacters() ([]Character, error) {
	var out []Character
	if err := c.doJSON(http.MethodGet, "/api/characters", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// RequestAccess asks for per-character access to the given ESI scopes and
// returns its status: "granted" or "pending". Like RequestPermissions,
// re-calling it polls for the user's approval.
func (c *Client) RequestAccess(characterID int64, scopes ...string) (string, error) {
	body := map[string]any{"character_id": characterID, "scopes": scopes}
	var out struct {
		Status string `json:"status"`
	}
	if err := c.doJSON(http.MethodPost, "/api/request-access", body, &out); err != nil {
		return "", err
	}
	return out.Status, nil
}

// ProxyResult is a decoded ESI passthrough response plus the Expires instant
// the proxy relayed from ESI — the moment the cached copy goes stale, which
// is exactly when this app should refetch.
type ProxyResult struct {
	Expires time.Time
}

// proxyGet performs GET /proxy/{characterID}/{esiPath} and decodes the JSON
// body into out (which may be nil to discard it). It returns the response's
// Expires header so the caller can schedule its next refresh on the ESI cache
// timeout. esiPath is an ESI path such as "characters/123/location/".
func (c *Client) proxyGet(characterID int64, esiPath string, out any) (ProxyResult, error) {
	path := fmt.Sprintf("/proxy/%d/%s", characterID, strings.TrimLeft(esiPath, "/"))
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return ProxyResult{}, err
	}
	req.Header.Set("X-Proxy-Api-Key", c.APIKey)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ProxyResult{}, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return ProxyResult{}, err
	}
	if resp.StatusCode >= 400 {
		return ProxyResult{}, fmt.Errorf("GET %s: %s: %s", path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return ProxyResult{}, fmt.Errorf("decode %s: %w", path, err)
		}
	}
	return ProxyResult{Expires: parseExpires(resp.Header.Get("Expires"))}, nil
}

// Location is the character's current location (ESI /characters/{id}/location/).
// Exactly one of StationID / StructureID is set when docked; neither when in
// space.
type Location struct {
	SolarSystemID int64 `json:"solar_system_id"`
	StationID     int64 `json:"station_id"`
	StructureID   int64 `json:"structure_id"`
}

// Ship is the character's current ship (ESI /characters/{id}/ship/).
type Ship struct {
	ItemID int64  `json:"ship_item_id"`
	Name   string `json:"ship_name"`
	TypeID int64  `json:"ship_type_id"`
}

// Location fetches the character's current location.
func (c *Client) Location(characterID int64) (Location, ProxyResult, error) {
	var loc Location
	res, err := c.proxyGet(characterID, fmt.Sprintf("characters/%d/location/", characterID), &loc)
	return loc, res, err
}

// Ship fetches the character's current ship.
func (c *Client) Ship(characterID int64) (Ship, ProxyResult, error) {
	var ship Ship
	res, err := c.proxyGet(characterID, fmt.Sprintf("characters/%d/ship/", characterID), &ship)
	return ship, res, err
}

// Names resolves ESI IDs (solar systems, stations, structures, ship types) to
// human-readable names via the proxy, caching each lookup. These are public
// (or already-scoped, for structures) ESI endpoints that all return {"name":
// ...}; going through the proxy means they share its cache too.
type Names struct {
	client *Client
	charID int64
	cache  map[string]string // esiPath -> name
}

func newNames(c *Client, characterID int64) *Names {
	return &Names{client: c, charID: characterID, cache: map[string]string{}}
}

// lookup returns the name for an ESI entity path, or the fallback string if it
// can't be resolved (resolution is best-effort — the watcher should keep
// working even when a name lookup fails).
func (n *Names) lookup(esiPath, fallback string) string {
	if name, ok := n.cache[esiPath]; ok {
		return name
	}
	var out struct {
		Name string `json:"name"`
	}
	if _, err := n.client.proxyGet(n.charID, esiPath, &out); err != nil || out.Name == "" {
		return fallback
	}
	n.cache[esiPath] = out.Name
	return out.Name
}

func (n *Names) system(id int64) string {
	return n.lookup(fmt.Sprintf("universe/systems/%d/", id), fmt.Sprintf("system %d", id))
}

func (n *Names) station(id int64) string {
	return n.lookup(fmt.Sprintf("universe/stations/%d/", id), fmt.Sprintf("station %d", id))
}

func (n *Names) structure(id int64) string {
	return n.lookup(fmt.Sprintf("universe/structures/%d/", id), fmt.Sprintf("structure %d", id))
}

func (n *Names) shipType(id int64) string {
	return n.lookup(fmt.Sprintf("universe/types/%d/", id), fmt.Sprintf("type %d", id))
}

// doJSON performs an HTTP request with an optional JSON body and decodes a
// JSON response into out. It sets the api_key header whenever one is set.
func (c *Client) doJSON(method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.APIKey != "" {
		req.Header.Set("X-Proxy-Api-Key", c.APIKey)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(data)))
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			return fmt.Errorf("decode %s %s: %w", method, path, err)
		}
	}
	return nil
}

// parseExpires parses an HTTP Expires header, returning the zero time if it's
// missing or unparsable (callers treat that as "refetch on the fallback
// interval").
func parseExpires(v string) time.Time {
	if v == "" {
		return time.Time{}
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return time.Time{}
	}
	return t
}
