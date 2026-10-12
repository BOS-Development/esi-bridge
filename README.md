# ESI Proxy

Proxies all Eve Online ESI connections, allowing applications to access character lists and ESI endpoints without needing to be explicitly added.  

## Primary Workflow

* User adds character to proxy via ESI
* User can define which accounts characters belong too
* User configures external application (MCO, Pyfa) to use the proxy
* External Application makes a request to use the proxy, the user is prompted to approve and to list what data the application is allowed to see
* External Applications use the proxy for all requests to ESI
* Includes endpoints to list accounts & characters
* Proxy caches all ESI responses, and provides a cached response unless the ESI cache is expired for the endpoint
* Proxy maintains access tokens

## Implementation

* Cross-Platform, Windows, MacOS and Linux
* SQLite
* Ability to copy token information from one computer to another
* Background task to maintain tokens and handle proxy calls
* Simple UI to manage characters, accounts and existing permissions

## Endpoints

* Full Mirror of ESI endpoints
* List Accounts
* List Characters
* List application permissions

## Getting Started

esi-proxy is a single Go binary with a system tray UI — no separate services to run.

### 1. Register an EVE SSO application

Go to [developers.eveonline.com](https://developers.eveonline.com) and create an application:

* Connection Type: **Authentication & API Access**
* Callback URL: `eveauth-proxy://callback` — a custom URL scheme, not an `http://` address (matches the convention other EVE community tools use, e.g. `eveauth-pyfa`). esi-proxy registers itself as this scheme's OS handler on every startup; see the platform notes below.
* Scopes: whatever ESI scopes you want the proxy able to request on your characters' behalf (see `server.LoginScopes` in `internal/server/sso.go` for the defaults)

Copy the generated **Client ID**.

### 2. Build and run

**Windows / Linux:**

```sh
go build -o esi-proxy ./cmd/esi-proxy
./esi-proxy
```

**macOS:** custom URL schemes can only be routed to a proper `.app` bundle — Launch Services has no way to associate one with a bare binary — so build and run via:

```sh
./scripts/build-macos-app.sh
open "ESI Proxy.app"
```

(`go build -o esi-proxy ./cmd/esi-proxy && ./esi-proxy` still works fine on macOS for everything *except* SSO login, which needs the bundle.)

A tray icon appears. Click **Set EVE Client ID** — it opens a small local page in your browser to paste in the Client ID from step 1; it's saved and takes effect immediately, no restart needed. Then use **Add Character** to log in via EVE SSO. Note your browser will likely show a one-time "Open ESI Proxy?" permission prompt before handing off to the app — that's expected custom-scheme behavior, not a bug, and afterwards you'll get a desktop notification confirming the character was added (the browser tab itself can't show much once the handoff happens).

(Config lives in a file the app manages for you — macOS: `~/Library/Application Support/esi-proxy/config.json`, Linux: `~/.config/esi-proxy/config.json`, Windows: `%AppData%\esi-proxy\config.json` — you shouldn't need to touch it directly.)

### 3. Point an external application at the proxy

The proxy listens on `localhost:8085` by default. An app can either use that address directly, or discover it over mDNS (see [Discovery & network access](#discovery--network-access) below) so the user never has to type a host:port.

1. The app registers itself: `POST http://localhost:8085/api/register {"name": "Pyfa"}` → returns an `api_key`.
2. To discover characters/accounts rather than being told a character_id out of band, the app requests app-wide permissions: `POST http://localhost:8085/api/request-permissions` with header `X-Proxy-Api-Key: <api_key>` and body `{"permissions": ["list_characters", "list_accounts"]}`. Approve or deny each from the tray's **Permission Requests** menu.
   * `list_characters` unlocks `GET /api/characters` returning every character on the proxy (id + name) instead of just ones it already has a per-character grant for.
   * `list_accounts` unlocks `GET /api/accounts` (id + name), and adds `account_id` to each entry from `/api/characters`. Neither is granted by default — account grouping is private, user-managed data ESI itself never exposes.
3. The app requests access to a specific character: `POST http://localhost:8085/api/request-access` with the same header and body `{"character_id": 12345, "scopes": [...]}`.
4. Approve or deny that request from the tray's **Pending Approvals** menu.
5. The app then calls ESI through the proxy: `GET http://localhost:8085/proxy/12345/<esi-path>` with the same `X-Proxy-Api-Key` header. It can check its current character-level access at any time via `GET /api/grants` (id, name, and the specific scopes granted).

### Discovery & network access

So that external apps don't have to hard-code `localhost:8085`, the proxy advertises itself on the local network over **mDNS / DNS-SD** (the same "Bonjour"/Zeroconf mechanism printers use) while it's running.

* **Service type:** `_esi-proxy._tcp` (domain `local.`)
* **TXT records:**
  * `base_url` — the URL the app should actually connect to, e.g. `http://localhost:8085`. **Use this value directly; don't rebuild the URL from the resolved A record.** When the proxy is bound to localhost (the default), the LAN address in the A record isn't reachable — `base_url` is the authoritative answer.
  * `path` — the base path (currently `/`).

An app discovers the proxy by browsing for `_esi-proxy._tcp` and reading `base_url`; everything in step 3 then proceeds against that URL. If the host has no working multicast, advertising is skipped (logged as a warning) and the proxy still works — apps just fall back to the default address.

#### Letting other machines connect (LAN access)

By default the proxy binds to `localhost`, so only applications **on the same machine** can connect — the token-holding proxy stays off the network. To let other machines on your LAN connect to a proxy they discover, enable **Allow LAN Access** in the tray menu. This binds to all interfaces and advertises your machine's `hostname.local` address instead of `localhost`.

The listener is bound once at startup, so **toggling LAN access takes effect on the next restart** (the tray notification reminds you). You can also set it directly in `config.json` via `"bind_host"`: `"localhost"` (default, same-machine only), `"0.0.0.0"` (all interfaces), or a specific interface IP.

> **Note:** exposing the proxy to the LAN means any machine that can reach it can drive the app-registration/approval flow, so only enable it on networks you trust, and keep an eye on the tray's **Pending Approvals** and **Permission Requests** when you do.

### Copying tokens to another computer

```sh
./esi-proxy export --out=bundle.enc     # prompts for a passphrase
./esi-proxy import --in=bundle.enc      # on the other machine, same passphrase
```

### Running tests

```sh
go test ./...
```
