# location-watcher — example ESI Bridge client

A small, self-contained example of an **external application** talking to a
running [ESI Bridge](../../README.md). It shows the full flow a real app goes
through — discovery, registration, approval, and reading ESI through the
proxy — in nothing but Go's standard library and the proxy's public HTTP API.
(It deliberately imports none of the proxy's `internal/` packages, since a
third-party app couldn't either.)

## What it does

1. **Detects the proxy** by calling `GET /healthz`.
2. **Registers** itself (`POST /api/register`) and **requests the
   `list_characters` permission** (`POST /api/request-permissions`), then waits
   for you to approve it from the proxy's tray/window.
3. **Lists the characters** (`GET /api/characters`) and lets you pick one.
4. **Requests that character's location and ship scopes**
   (`POST /api/request-access`) and waits for approval.
5. **Displays the character's ship and location**, refreshing each on its own
   ESI **cache timeout**: the proxy relays ESI's `Expires` header, so the app
   refetches exactly when the cached data can next change — no fixed polling
   interval, no hammering ESI.

The issued `api_key` is saved under your OS config directory
(`esi-proxy-example/state.json`), so later runs reuse the same registration and
you only approve the app once. Pass `-reset` to forget it and register anew.

## Build & run

The proxy must already be running, with at least one character added.

```sh
# Linux
./scripts/build-example-linux.sh && ./location-watcher

# macOS (no .app bundle needed — this example registers no URL scheme)
./scripts/build-example-macos.sh && ./location-watcher

# Windows (PowerShell)
.\scripts\build-example-windows.ps1 ; .\location-watcher.exe
```

Or just `go run ./examples/location-watcher`.

### Flags

| Flag     | Default                 | Meaning                                              |
| -------- | ----------------------- | ---------------------------------------------------- |
| `-proxy` | `http://localhost:8085` | Base URL of the proxy to connect to.                 |
| `-name`  | `ESI Location Watcher`  | Name this app registers under (shown in approvals).  |
| `-poll`  | `2s`                    | How often to re-check for your approval while waiting. |
| `-reset` | `false`                 | Forget the saved api_key for this proxy and re-register. |

When prompted, approve the permission and access requests from the proxy's
tray menu (**Permission Requests** / **Pending Approvals**) or its management
window.

## Example session

```
Looking for ESI Bridge at http://localhost:8085 ... found.
Waiting for you to approve the "list_characters" permission ...
Permission "list_characters" granted.

Characters on the proxy:
   1. Jita Trader
   2. Null Ratter
Select a character [1-2]: 2
Waiting for you to approve access to Null Ratter ...
Access to Null Ratter granted.

Watching Null Ratter — refreshing on each ESI cache timeout. Press Ctrl-C to stop.

[18:42:07] Null Ratter
  Ship:     My Ratting Ishtar (Ishtar)
  System:   VFK-IV
  Docked:   (in space)
  (next refresh in 5s)
```
