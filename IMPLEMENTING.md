# Implementing ESI Bridge support in your application

This guide is for developers of third-party EVE Online tools (fitting tools,
multiboxing helpers, trackers, and so on) who want to read ESI data through
**ESI Bridge** instead of running their own EVE SSO flow.

With the proxy, your app never sees a refresh token, never registers an SSO
application, and never handles a login callback. The user adds characters to
the proxy once. Your app asks the proxy for access, the user approves it in the
proxy's window, and from then on your app calls ESI through the proxy using a
single API key.

For a complete, working client that uses only Go's standard library, see
[`examples/location-watcher`](examples/location-watcher/).

---

## Contents

1. [How it works](#1-how-it-works)
2. [Finding the proxy](#2-finding-the-proxy)
3. [Registering your app](#3-registering-your-app)
4. [App-wide permissions: listing characters and accounts](#4-app-wide-permissions-listing-characters-and-accounts)
5. [Character access and scopes](#5-character-access-and-scopes)
6. [Calling ESI through the proxy](#6-calling-esi-through-the-proxy)
7. [Caching and refresh timing](#7-caching-and-refresh-timing)
8. [Errors and how to handle them](#8-errors-and-how-to-handle-them)
9. [API reference](#9-api-reference)
10. [Implementation checklist](#10-implementation-checklist)

---

## 1. How it works

```
┌────────────┐   X-Proxy-Api-Key    ┌────────────┐   Bearer <token>   ┌─────┐
│  Your app  │ ───────────────────▶ │ ESI Bridge │ ─────────────────▶ │ ESI │
└────────────┘ ◀─────────────────── └────────────┘ ◀───────────────── └─────┘
                 ESI response,          │  ▲
                 relayed unchanged      ▼  │ user approves/denies
                                    ┌────────────┐
                                    │ Proxy UI   │
                                    └────────────┘
```

The integration has four steps:

1. **Find** the proxy (default `http://localhost:8085`, or discover it over mDNS).
2. **Register** once to get an `api_key`. Store the key and reuse it.
3. **Request** what you need. The user approves or denies each request in the
   proxy's **Requests** view. There are two kinds of request:
   * *app-wide permissions*, such as seeing the full character list
   * *per-character access* to a set of ESI scopes
4. **Call ESI** at `/proxy/{character_id}/{esi_path}`. The proxy adds the
   character's access token, forwards the call to ESI, caches the response, and
   relays it back to you.

The proxy manages tokens for you. It refreshes access tokens in the background,
so your app never needs to handle a `401` from expired tokens.

---

## 2. Finding the proxy

### Default address

The proxy listens on **`http://localhost:8085`** by default. Let the user
override this in your app's settings: the port can be changed in the proxy's
config, and the proxy may be running on another machine on the LAN.

### mDNS / DNS-SD discovery (recommended)

When the proxy is running, it advertises itself on the local network:

| Field        | Value                                                    |
| ------------ | -------------------------------------------------------- |
| Service type | `_esi-proxy._tcp`                                        |
| Domain       | `local.`                                                 |
| Instance     | `ESI Bridge (<hostname>)`                                 |
| TXT `base_url` | The URL to connect to, e.g. `http://localhost:8085`    |
| TXT `path`   | Base path, currently `/`                                 |

> **Use the `base_url` TXT value exactly as given.** Do not build a URL from the
> resolved SRV/A record. By default the proxy only binds to `localhost`, so the
> LAN address in the A record will not accept connections. `base_url` is always
> the correct address. When the user turns on **Allow LAN Access**, `base_url`
> changes to a reachable LAN address such as `http://192.168.1.20:8085`.

If you find more than one instance (for example, the user runs the proxy on two
PCs), let the user pick one by its instance name.

If discovery finds nothing, fall back to `http://localhost:8085`. Not every
network supports multicast.

### Health check

Before you do anything else, confirm the proxy is running:

```http
GET /healthz
```

`200 OK` with body `ok` means the proxy is up. If the connection is refused,
tell the user to start ESI Bridge.

---

## 3. Registering your app

```http
POST /api/register
Content-Type: application/json

{"name": "My Fitting Tool"}
```

Response `201 Created`:

```json
{"application_id": 7, "api_key": "064b58ede99457379b787bc90c060bb7670ac6d17d38d9f4d09f08756e7aea60"}
```

* `name` is shown to the user in every approval prompt. Use your app's real,
  recognisable name.
* **The `api_key` is returned only once.** The proxy stores only a hash of it,
  so a lost key cannot be recovered. Store the key persistently in your app's
  config or the OS credential store, **keyed by proxy `base_url`**.
* Send the key on every later request in the header **`X-Proxy-Api-Key`**.
* Register **once per install**, not on every launch. Every approval the user
  gives is tied to the key. If you register again, you get a new, separate app
  entry with no permissions, and the user has to approve everything again.
  Names don't need to be unique: two installs of the same app are two separate
  entries.
* A new key has **no access at all** until the user approves something.

If any request returns `401 invalid api key` (for example, the user moved to a
new proxy database), discard the stored key, register again, and request your
permissions again.

---

## 4. App-wide permissions: listing characters and accounts

Without app-wide permissions, `GET /api/characters` returns only the characters
your app already has per-character access to. A new app sees an empty list.
To let the user choose a character inside your app, request `list_characters`.

| Permission        | Unlocks                                                                 |
| ----------------- | ----------------------------------------------------------------------- |
| `list_characters` | `GET /api/characters` returns **every** character on the proxy (id + name). |
| `list_accounts`   | `GET /api/accounts`, and adds `account_id` to each `/api/characters` entry. |

Accounts are groups of characters that the user creates in the proxy. ESI does
not expose this data, so ask for `list_accounts` only if your app needs to show
characters grouped by account (for example, a multiboxing tool).

```http
POST /api/request-permissions
X-Proxy-Api-Key: <api_key>
Content-Type: application/json

{"permissions": ["list_characters", "list_accounts"]}
```

The response maps each permission to its status:

```json
{"list_characters": "granted", "list_accounts": "pending"}
```

* `200 OK`: every requested permission is already granted.
* `202 Accepted`: at least one permission is `pending` and waiting for the user.
* `400`: unknown permission name. Only the two names above are accepted.

To check for approval, call the same endpoint again (see
[Waiting for approval](#waiting-for-approval)).

Then list characters:

```http
GET /api/characters
X-Proxy-Api-Key: <api_key>
```

```json
[
  {"id": 1978535095, "name": "Jita Trader", "account_id": 1},
  {"id": 2112000001, "name": "Null Ratter", "account_id": 2}
]
```

`account_id` is only included if `list_accounts` is granted.

```http
GET /api/accounts
X-Proxy-Api-Key: <api_key>
```

```json
[{"id": 1, "name": "Main"}, {"id": 2, "name": "Alts"}]
```

This returns `403` if `list_accounts` has not been granted.

---

## 5. Character access and scopes

Before you can call ESI for a character, the user must grant your app access to
that character. Request access along with the ESI scopes you intend to use:

```http
POST /api/request-access
X-Proxy-Api-Key: <api_key>
Content-Type: application/json

{
  "character_id": 1978535095,
  "scopes": ["esi-location.read_location.v1", "esi-location.read_ship_type.v1"]
}
```

| Status         | Body                      | Meaning                                                   |
| -------------- | ------------------------- | --------------------------------------------------------- |
| `200 OK`       | `{"status": "granted"}`   | You already hold a grant covering **all** these scopes.   |
| `202 Accepted` | `{"status": "pending"}`   | A request is waiting in the proxy UI.                     |
| `404`          | `unknown character_id`    | That character isn't on the proxy.                        |

### Choosing scopes

* **Always send the complete set of scopes your app needs for that character,
  not just the ones you are adding.** When the user approves a request, the
  scopes in that request *replace* the grant's scopes. If you already hold
  `A` and later request only `B`, approving the request leaves you with `B` only.
* Request only what you use. The user sees the full list when approving, and
  can later narrow your grant per character in the proxy UI.
* The proxy can only serve scopes that the character's own token holds, which
  the proxy requested when the character was added. If you request a scope the
  token lacks, ESI returns `403` on the matching endpoints. The default set is
  `server.LoginScopes` in [internal/server/sso.go](internal/server/sso.go).
* Public ESI endpoints such as `universe/types/{id}/` need no scope, but you
  still call them through a character path (see [§6](#6-calling-esi-through-the-proxy)),
  so you still need a grant for that character.

### Checking current access

```http
GET /api/grants
X-Proxy-Api-Key: <api_key>
```

```json
[
  {
    "character_id": 1978535095,
    "character_name": "Jita Trader",
    "scopes": ["esi-location.read_location.v1", "esi-location.read_ship_type.v1"]
  }
]
```

This endpoint only reads. It never creates a request, so it is the right way to
check your access at startup and to detect that the user revoked access or
removed a character.

### Waiting for approval

There is no push notification or webhook. To learn whether the user approved a
request, poll for it:

* **Character access:** poll `GET /api/grants` until the character appears
  with the scopes you need. Polling `/api/request-access` also works, but each
  call re-submits the request, which brings a denied request back into the UI.
* **App-wide permissions:** there is no read-only status endpoint, so call
  `POST /api/request-permissions` again. If a permission is already granted,
  the response says `granted` and nothing new is created.

Guidelines:

* Poll every **2–5 seconds** while your UI is waiting. Stop once the request is
  granted.
* **A denied request looks the same as a pending one.** Denying a request just
  deletes it, so `/api/grants` never shows the character. Put a time limit on
  your waiting UI (for example, 2–5 minutes). When it expires, offer a *"Request
  again"* button rather than re-submitting in a loop.
* Show the user what to do, for example: *"Approve **My Fitting Tool** in ESI
  Proxy → Requests."* If the proxy window is closed, it also shows a desktop
  notification.

### Revocation

The user can revoke a grant, narrow its scopes, revoke an app-wide permission,
or delete a character at any time. Your app must handle this:

* Proxy calls for a revoked or deleted character return `403` (see [§8](#8-errors-and-how-to-handle-them)).
* `/api/characters` and `/api/grants` stop listing it.

---

## 6. Calling ESI through the proxy

```
{base_url}/proxy/{character_id}/{esi_path}
```

* `character_id`: the character whose token authenticates the call. You need
  an active grant for this character.
* `esi_path`: the ESI path **without** the host or version prefix. The proxy
  forwards to `https://esi.evetech.net/latest`, so
  `characters/1978535095/location/` becomes
  `https://esi.evetech.net/latest/characters/1978535095/location/`.
* Keep ESI's trailing slashes. The query string is forwarded unchanged.
* Any HTTP method works (`GET`, `POST`, `PUT`, `DELETE`). The request body is
  forwarded unchanged.

```http
GET /proxy/1978535095/characters/1978535095/location/
X-Proxy-Api-Key: <api_key>
```

```json
{"solar_system_id": 30000142, "station_id": 60003760}
```

More examples:

```http
# Wallet balance
GET  /proxy/1978535095/characters/1978535095/wallet/

# Paged endpoint: pass ESI's own query parameters through
GET  /proxy/1978535095/characters/1978535095/assets/?page=2

# Public data, routed through any character you have a grant for
GET  /proxy/1978535095/universe/systems/30000142/

# Write endpoint: query and body are forwarded
POST /proxy/1978535095/ui/autopilot/waypoint/?destination_id=30000142&add_to_beginning=false&clear_other_waypoints=true
```

### What the proxy changes and what it doesn't

* **Response:** ESI's status code, body, and headers are relayed unchanged
  (except hop-by-hop headers). This includes `Expires`, `ETag`,
  `Last-Modified`, `X-Pages`, and the `X-Esi-Error-Limit-*` headers. An ESI
  error such as `404` or `420` is passed through as-is.
* **Request headers are not forwarded.** The proxy sends ESI only its own
  `Authorization` and `User-Agent` headers. Headers such as `If-None-Match`,
  `Accept-Language`, or `X-Compatibility-Date` will not reach ESI. To get data
  in another language, use ESI's query parameter instead, e.g. `?language=de`.
* **Don't send an `Authorization` header** of your own. The proxy ignores it.
* **The path's character must be the token's character.** For a character
  endpoint like `characters/{id}/...`, use the same ID in both places. If you
  put another character's ID in the ESI path, ESI rejects the call because the
  token belongs to the first character.

---

## 7. Caching and refresh timing

The proxy caches every successful (`< 400`) **GET** response in memory until
the time in ESI's `Expires` header. Cache entries are separate for each
character, path, and query string. Error responses and non-GET requests are
never cached.

What this means for your app:

* **Schedule refetches from the `Expires` header.** Before `Expires`, a
  refetch returns the same cached data. After `Expires`, it returns fresh data
  from ESI. Add a small floor (a few seconds) so a missing or past `Expires`
  can't make your app spin in a tight loop. `location-watcher` shows this pattern.
* You don't need your own cache to protect ESI. Repeated calls before `Expires`
  never reach ESI, and several apps reading the same character share the cache.
* The cache lives only in memory and is cleared when the proxy restarts.

---

## 8. Errors and how to handle them

The proxy's own errors are plain-text bodies. ESI errors are relayed with ESI's
JSON body. To tell them apart, check the `Content-Type` and status code.

| Status | Source | Cause                                                                    | What your app should do                                          |
| ------ | ------ | ------------------------------------------------------------------------ | ---------------------------------------------------------------- |
| `400`  | Proxy  | Bad JSON body, missing field, unknown permission, non-numeric `character_id` | Fix the request; this is a bug in your app.                         |
| `401`  | Proxy  | Missing or invalid `X-Proxy-Api-Key`                                     | Register again, store the new key, request access again.             |
| `403`  | Proxy  | No active grant for this character (on `/proxy/...`)                     | Request access ([§5](#5-character-access-and-scopes)) and tell the user. |
| `403`  | Proxy  | `list_accounts` not granted (on `/api/accounts`)                         | Request the permission, or hide account features.               |
| `403`  | ESI    | The character's token lacks the scope, or ESI denied the request in-game | Tell the user; the character may need to be re-added with more scopes. |
| `404`  | Proxy  | `unknown character_id` on `/api/request-access`                          | Refresh your character list.                                    |
| `429`  | Proxy  | The proxy paused ESI calls because ESI's error budget is nearly used up  | Back off and retry later (ESI's error window is at most 60 s).      |
| `502`  | Proxy  | Couldn't get a valid token (e.g. the refresh token was revoked), or ESI was unreachable | Retry with backoff; if it continues, ask the user to check the character in the proxy. |
| other  | ESI    | Any other ESI status, relayed unchanged                                  | Handle it as you would a direct ESI call.                            |

> **Note:** calling `/proxy/{character_id}/...` without a grant also creates a
> pending access request with **no scopes** for that character. Don't rely on
> this. Call `/api/request-access` with your real scope list, so the user can
> see what they are approving.

### Be a good ESI citizen

Every app using the proxy shares one ESI error budget. If your app keeps
sending requests that fail (bad IDs, endpoints it lacks scopes for, tight retry
loops), the proxy returns `429` to *every* app the user runs. Don't retry
`4xx` responses in a loop, and back off on `5xx` and `429`.

---

## 9. API reference

Every endpoint except `/healthz` and `/api/register` requires
`X-Proxy-Api-Key: <api_key>`. Send request bodies as JSON.

| Method & path                         | Body                                         | Success response                                                |
| ------------------------------------- | -------------------------------------------- | --------------------------------------------------------------- |
| `GET /healthz`                        | none                                         | `200` `ok`                                                      |
| `POST /api/register`                  | `{"name": string}`                           | `201` `{"application_id": int, "api_key": string}`              |
| `POST /api/request-permissions`       | `{"permissions": ["list_characters" \| "list_accounts", ...]}` | `200`/`202` `{"<perm>": "granted" \| "pending", ...}` |
| `GET /api/characters`                 | none                                         | `200` `[{"id": int, "name": string, "account_id"?: int}]`       |
| `GET /api/accounts`                   | none                                         | `200` `[{"id": int, "name": string}]` (needs `list_accounts`)   |
| `POST /api/request-access`            | `{"character_id": int, "scopes": [string]}`  | `200` `{"status": "granted"}` / `202` `{"status": "pending"}`   |
| `GET /api/grants`                     | none                                         | `200` `[{"character_id": int, "character_name": string, "scopes": [string]}]` |
| `ANY /proxy/{character_id}/{esi_path}`| forwarded to ESI                             | ESI's response, relayed                                         |

The `/app`, `/manage/*`, `/setup/*`, and `/sso/*` routes are for the proxy's own
UI. Only local UI code holding the session token can use them. Don't call
them from your app.

### Example: the full flow with curl

```sh
BASE=http://localhost:8085

# 1. Register once and save the key
KEY=$(curl -s -X POST $BASE/api/register \
  -H "Content-Type: application/json" \
  -d '{"name": "My Tool"}' | jq -r .api_key)

# 2. Ask to see the character list, then approve it in the proxy UI
curl -s -X POST $BASE/api/request-permissions \
  -H "X-Proxy-Api-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"permissions": ["list_characters"]}'

# 3. List characters
curl -s $BASE/api/characters -H "X-Proxy-Api-Key: $KEY"

# 4. Request access to one character, then approve it in the proxy UI
curl -s -X POST $BASE/api/request-access \
  -H "X-Proxy-Api-Key: $KEY" -H "Content-Type: application/json" \
  -d '{"character_id": 1978535095, "scopes": ["esi-location.read_location.v1"]}'

# 5. Confirm, then call ESI
curl -s $BASE/api/grants -H "X-Proxy-Api-Key: $KEY"
curl -si $BASE/proxy/1978535095/characters/1978535095/location/ \
  -H "X-Proxy-Api-Key: $KEY"
```

### Example: minimal Python client

```python
import time
import requests

class EsiBridge:
    def __init__(self, base_url="http://localhost:8085", api_key=None):
        self.base = base_url.rstrip("/")
        self.s = requests.Session()
        if api_key:
            self.s.headers["X-Proxy-Api-Key"] = api_key

    def register(self, name):
        r = self.s.post(f"{self.base}/api/register", json={"name": name})
        r.raise_for_status()
        key = r.json()["api_key"]
        self.s.headers["X-Proxy-Api-Key"] = key
        return key  # store this persistently!

    def request_access(self, character_id, scopes):
        r = self.s.post(f"{self.base}/api/request-access",
                        json={"character_id": character_id, "scopes": scopes})
        r.raise_for_status()
        return r.json()["status"]

    def wait_for_grant(self, character_id, scopes, timeout=300, interval=3):
        deadline = time.time() + timeout
        while time.time() < deadline:
            r = self.s.get(f"{self.base}/api/grants")
            r.raise_for_status()
            for g in r.json():
                if g["character_id"] == character_id and set(scopes) <= set(g["scopes"]):
                    return True
            time.sleep(interval)
        return False  # denied or ignored; offer "Request again"

    def esi(self, character_id, path, method="GET", **kw):
        return self.s.request(method, f"{self.base}/proxy/{character_id}/{path.lstrip('/')}", **kw)


proxy = EsiBridge(api_key=load_saved_key())          # your own persistence
scopes = ["esi-location.read_location.v1"]
if proxy.request_access(1978535095, scopes) == "pending":
    print("Approve 'My Tool' in ESI Bridge → Requests")
    proxy.wait_for_grant(1978535095, scopes)
resp = proxy.esi(1978535095, "characters/1978535095/location/")
print(resp.json(), "fresh until", resp.headers.get("Expires"))
```

---

## 10. Implementation checklist

- [ ] Discover the proxy over mDNS (`_esi-proxy._tcp`, use the `base_url` TXT
      value), fall back to `http://localhost:8085`, and let the user override it.
- [ ] Check `GET /healthz` and show a clear "start ESI Bridge" message on failure.
- [ ] Register **once**, and store the `api_key` persistently, keyed by `base_url`.
- [ ] On `401`, register again and request permissions again.
- [ ] Request `list_characters` (and `list_accounts` only if needed) to build a
      character picker.
- [ ] Request per-character access with the **complete** scope list your
      feature needs.
- [ ] Poll `GET /api/grants` for approval, with a timeout and a "request again"
      option, and tell the user where to approve.
- [ ] Build proxy URLs as `/proxy/{character_id}/{esi_path}`, with no
      `/latest` prefix and with ESI's trailing slashes.
- [ ] Schedule refreshes from the relayed `Expires` header.
- [ ] Handle `403` (revoked access), `429` (back off), and `502` (token or
      upstream problem) without retrying in a loop.
- [ ] Don't depend on request headers reaching ESI.
