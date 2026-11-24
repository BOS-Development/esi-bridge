# ESI Bridge

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

ESI Bridge is a single Go binary with a desktop app window (backed by a small background tray/menu-bar presence) — no separate services to run.

### Installing a release

Every push to `main` publishes installers on the [Releases](https://github.com/BOS-Development/esi-proxy/releases) page, built with the project's EVE SSO Client ID:

* **Windows** — `esi-proxy-<version>-windows-x64-setup.exe`. Installs for the current user (no admin prompt), adds a Start menu shortcut, and optionally a desktop shortcut and start-at-sign-in. Needs the WebView2 runtime, which current Windows 10/11 already has. The installer isn't code-signed yet, so SmartScreen may warn: choose **More info → Run anyway**. Uninstalling keeps your accounts and tokens in `%APPDATA%\esi-proxy`.
* **macOS** — `esi-proxy-<version>-macos-universal.dmg` (Apple Silicon and Intel, macOS 11+). Open it and drag **ESI Bridge** into **Applications**. The app isn't notarized yet, so the first launch is blocked: open **System Settings → Privacy & Security** and click **Open Anyway**.

#### Updates

Installed copies check the Releases page for a new version at startup and every 6 hours (turn this off under **Settings → Updates**). When one is out, you get a desktop notification, an **Install update** item in the tray menu, and an Updates panel in Settings. Nothing installs until you click it. The app then downloads the installer, checks it against the SHA-256 digest GitHub recorded for the upload, closes, installs and restarts on the new version. On Windows this only works for copies installed with the setup program. On macOS the app must be in a folder you can write to (such as Applications), not running from the disk image. Otherwise the app links you to the release page. Builds without a version (local `go build`) never check.

To build the installers yourself, put the Client ID in `.clientid` (see below) and run `.\scripts\package-windows.ps1 -Version 1.0.0` (needs [Inno Setup 6](https://jrsoftware.org/isinfo.php)) or `./scripts/package-macos.sh 1.0.0`. Output goes to `dist/`.

### 1. (Optional) Register your own EVE SSO application

ESI Bridge ships with a built-in EVE SSO application, so you can skip this step. If you'd rather log in through an application you control, go to [developers.eveonline.com](https://developers.eveonline.com) and create one:

* Connection Type: **Authentication & API Access**
* Callback URL: `eveauth-proxy://callback` — a custom URL scheme, not an `http://` address (matches the convention other EVE community tools use, e.g. `eveauth-pyfa`). ESI Bridge registers itself as this scheme's OS handler on every startup; see the platform notes below.
* Scopes: whatever ESI scopes you want the proxy able to request on your characters' behalf (see `server.LoginScopes` in `internal/server/sso.go` for the defaults)

Copy the generated **Client ID**. Characters are tied to the Client ID they were added with, so pick one before adding characters (switching later means re-adding them).

The built-in Client ID is injected at build time: the `scripts/build-*` scripts read it from a `.clientid` file at the repo root (gitignored — create it with just the Client ID inside) and pass it to `go build` as `-ldflags "-X github.com/BOS-Development/esi-proxy/internal/buildinfo.BundledClientID=<id>"`. Without that file the build has no bundled ID, and users must set a custom one.

### 2. Build and run

Building needs a C toolchain (`CGO_ENABLED=1`): the app window embeds the
platform's native webview, and the tray/menu-bar presence is native too.

* **macOS** — Xcode command-line tools (`xcode-select --install`). WebKit ships with the OS.
* **Linux** — GTK 3 and WebKitGTK dev packages, e.g. `libgtk-3-dev` and `libwebkit2gtk-4.1-dev` (Debian/Ubuntu).
* **Windows** — the [WebView2 runtime](https://developer.microsoft.com/microsoft-edge/webview2/) (preinstalled on current Windows 10/11); build with a C compiler such as the one from MSYS2/MinGW or TDM-GCC.

**Windows:**

```powershell
.\scripts\build-windows.ps1
.\esi-proxy.exe
```

**Linux:**

```sh
./scripts/build-linux.sh
./esi-proxy
```

**macOS:** custom URL schemes can only be routed to a proper `.app` bundle — Launch Services has no way to associate one with a bare binary — so build and run via:

```sh
./scripts/build-macos-app.sh
open "ESI Bridge.app"
```

(`go build -o esi-proxy ./cmd/esi-proxy && ./esi-proxy` still works fine on macOS for everything *except* SSO login, which needs the bundle.)

The Windows and Linux scripts are thin wrappers around `go build -o esi-proxy[.exe] ./cmd/esi-proxy` — feel free to invoke that directly instead.

The app icon lives in `internal/assets/` (`bridge.ico`, `bridge.png`) and is embedded into the tray, notifications and UI. The Windows exe icon comes from the committed `cmd/esi-proxy/rsrc_windows_amd64.syso`; after changing `bridge.ico`, regenerate it with `scripts/gen-winres.sh` (needs `windres` from MinGW/MSYS2).

Builds are **development builds** by default: they add a **Developer** section to the Settings tab, which can send a test desktop notification. For a **production release**, add the `release` build tag (`go build -tags release ./cmd/esi-proxy`, or `-Release` / `RELEASE=1` with the scripts), which leaves the Developer section out.

The **ESI Bridge** window opens (and a tray/menu-bar icon appears in the background; closing the window leaves the app running there, and **Open ESI Bridge** in that menu re-opens it). If you registered your own application in step 1, open **Settings**, tick **Use a custom EVE SSO Client ID**, and paste its Client ID — it's saved and takes effect immediately, no restart needed. Then use **Add Character** to log in via EVE SSO. Note your browser will likely show a one-time "Open ESI Bridge?" permission prompt during SSO login — that's expected custom-scheme behavior, not a bug — and afterwards the character appears in the window.

The management UI is served over a loopback-only HTTP endpoint, so it also remains reachable in a browser at `http://localhost:8085/app` if you prefer that.

(Config lives in a file the app manages for you — macOS: `~/Library/Application Support/esi-proxy/config.json`, Linux: `~/.config/esi-proxy/config.json`, Windows: `%AppData%\esi-proxy\config.json` — you shouldn't need to touch it directly.)

### 3. Point an external application at the proxy

The proxy listens on `localhost:8085` by default. An app can either use that address directly, or discover it over mDNS (see [Discovery & network access](#discovery--network-access) below) so the user never has to type a host:port.

1. The app registers itself: `POST http://localhost:8085/api/register {"name": "Pyfa"}` → returns an `api_key`.
2. To discover characters/accounts rather than being told a character_id out of band, the app requests app-wide permissions: `POST http://localhost:8085/api/request-permissions` with header `X-Proxy-Api-Key: <api_key>` and body `{"permissions": ["list_characters", "list_accounts"]}`. Approve or deny each from the window's **Requests** view.
   * `list_characters` unlocks `GET /api/characters` returning every character on the proxy (id + name) instead of just ones it already has a per-character grant for.
   * `list_accounts` unlocks `GET /api/accounts` (id + name), and adds `account_id` to each entry from `/api/characters`. Neither is granted by default — account grouping is private, user-managed data ESI itself never exposes.
3. The app requests access to a specific character: `POST http://localhost:8085/api/request-access` with the same header and body `{"character_id": 12345, "scopes": [...]}`.
4. Approve or deny that request from the window's **Requests** view. (When the window is closed, a desktop notification lets you know a request is waiting.)
5. The app then calls ESI through the proxy: `GET http://localhost:8085/proxy/12345/<esi-path>` with the same `X-Proxy-Api-Key` header. It can check its current character-level access at any time via `GET /api/grants` (id, name, and the specific scopes granted).

#### Example application

[`examples/location-watcher`](examples/location-watcher/) is a small, runnable example client that walks this whole flow end to end — it detects the proxy, requests `list_characters`, lets you pick a character, requests that character's location/ship scopes, then displays the character's current ship and location, refreshing each on its ESI cache timeout (read from the relayed `Expires` header). It uses only the standard library and the public HTTP API, so it also serves as a copy-paste reference. Build it with `./scripts/build-example-<os>.sh` (or `go run ./examples/location-watcher`); see its [README](examples/location-watcher/README.md).

### Discovery & network access

So that external apps don't have to hard-code `localhost:8085`, the proxy advertises itself on the local network over **mDNS / DNS-SD** (the same "Bonjour"/Zeroconf mechanism printers use) while it's running.

* **Service type:** `_esi-proxy._tcp` (domain `local.`)
* **TXT records:**
  * `base_url` — the URL the app should actually connect to, e.g. `http://localhost:8085`. **Use this value directly; don't rebuild the URL from the resolved A record.** When the proxy is bound to localhost (the default), the LAN address in the A record isn't reachable — `base_url` is the authoritative answer.
  * `path` — the base path (currently `/`).

An app discovers the proxy by browsing for `_esi-proxy._tcp` and reading `base_url`; everything in step 3 then proceeds against that URL. If the host has no working multicast, advertising is skipped (logged as a warning) and the proxy still works — apps just fall back to the default address.

#### Letting other machines connect (LAN access)

By default the proxy binds to `localhost`, so only applications **on the same machine** can connect — the token-holding proxy stays off the network. To let other machines on your LAN connect to a proxy they discover, enable **Allow LAN access** under **Settings**. This binds to all interfaces and advertises your machine's LAN IP (e.g. `http://192.168.1.20:8085`) in `base_url` instead of `localhost`, falling back to `hostname.local` if no LAN address is found.

**Toggling LAN access takes effect immediately** — no restart. The listener is rebound and the mDNS advert re-registered on the spot; turning it off also disconnects machines already connected over the LAN (in-flight requests get a few seconds to finish). While LAN access is on, the proxy re-checks its LAN IP every 30 seconds and updates the advert if it changes (e.g. after a DHCP renewal or switching networks). You can also set it directly in `config.json` via `"bind_host"`: `"localhost"` (default, same-machine only), `"0.0.0.0"` (all interfaces), or a specific interface IP — edits there are picked up on the next start.

> **Note:** exposing the proxy to the LAN means any machine that can reach it can drive the app-registration/approval flow, so only enable it on networks you trust, and keep an eye on the window's **Requests** view when you do.

### Copying tokens to another computer

```sh
./esi-proxy export --out=bundle.enc     # prompts for a passphrase
./esi-proxy import --in=bundle.enc      # on the other machine, same passphrase
```

On an installed Windows copy, run these with `esi-proxy-cli.exe` from the install folder (`%LOCALAPPDATA%\Programs\ESI Bridge`). The main `esi-proxy.exe` there is built without a console, so it can't prompt for a passphrase.

### Running tests

```sh
go test ./...
```
