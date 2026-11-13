# ESI Proxy UI — design context

Design standards for the proxy's management UI (`internal/webui`). Read this
before changing `assets/index.html` or `assets/static/app.css`. The UI is a
local management app served over the loopback HTTP server — favor clarity and
density over decoration.

## Hard rules

- **No cards.** Do not group content in elevated, boxed containers — no
  per-section or per-row panels with their own background fill + border +
  corner radius + shadow. Structure comes from **typography and hairline
  dividers on a single flat surface**, not from stacked boxes. (Small inline
  tags/chips and form controls are not cards; see below.)
- **No gradients.** Every fill is a single flat color. No `linear-gradient`,
  `radial-gradient`, or gradient borders/text, anywhere, in either theme.
- **No shadows for grouping.** Flatness is the look; don't reintroduce a card
  by giving a block a drop shadow. (A transient overlay like the toast may use
  a hairline border to separate from content, not a shadow.)

## Layout & navigation

This is a multi-view app, **not** one long scrolling page. The chrome is
persistent; only the content pane changes.

- **Top bar** (full width, hairline bottom border): the app name on the left;
  the running-status text and the primary **Add Character** action on the
  right. Present on every view.
- **Sidebar** (left, hairline right border): one nav link per view —
  Requests, Characters, Grants, Permissions, Settings. The active link is
  marked with `--accent` text and a 2px `--accent` left edge, not a filled
  box. Action-bearing links (Requests) show a muted item count. On narrow
  widths the sidebar collapses to a horizontal, scrollable nav strip above the
  content.
- **Content pane** (`--bg`): shows exactly one view at a time, chosen by a
  hash route (`#/characters`, …); default `#/characters`. Each view opens with
  a page heading (`h1`, ~1.1rem) and then its content. The pane is the only
  scrolling region.

### Views

- **Characters** — the default, primary view: every character on the proxy,
  with its account and how many applications have access. Each row is a whole
  navigation target (→ character detail), marked with a chevron, not a button.
- **Requests** — the inbox of things awaiting the user: pending access
  requests and pending permission requests, each with Approve/Deny. Its nav
  item shows a count.
- **Grants** — all active per-character grants, each with Edit and Revoke.
- **Permissions** — app-wide permissions granted, each with Revoke.
- **Settings** — EVE SSO Client ID and LAN access.

### Drill-in (nested under Characters)

Navigation goes three levels deep under Characters; the sidebar keeps
Characters highlighted throughout, and each nested view starts with a `‹` back
link, never a browser-only back.

- **Character detail** (`#/characters/{id}`) — the character, a "Tokens" group
  showing the access-token expiry, when the token pair was last refreshed and
  when the character was added (relative ages, with absolute times on hover)
  plus a **Refresh tokens** button that forces an immediate SSO refresh, then a
  group "Applications with access" listing each application that holds a grant
  for it and the permissions granted. Per application: **Revoke** (drop all its
  access to this character) and **Edit permissions** (→ scope editor). A
  "Danger zone" group ends the page with **Delete character**, which removes
  the character (tokens and all grants) after a confirmation modal.
- **Scope editor** (`#/characters/{id}/grants/{grantId}`) — a checklist of the
  ESI permissions the application may use for this character: the character's
  available (token) scopes unioned with whatever the grant already holds, the
  granted ones ticked. Select-all / Clear, then **Save** (≥1 required),
  **Cancel**, or **Revoke access**. This view owns its state, so the
  background poll does not re-render it.

### Content within a view

- A view groups its rows under small uppercase, letter-spaced, muted group
  labels — separation by typography and a stronger rule, never a surrounding
  box.
- **Rows** (characters, grants, requests, …) sit directly on the page surface,
  separated by a single bottom **hairline divider** (`--border`); the last row
  in a group has none. A row may show a subtle `--bg-subtle` hover tint — not a
  border or shadow.
- Empty views/groups state so in plain muted text, not an illustrated card.

## Color tokens

Define all colors as CSS custom properties on `:root`, and redefine them under
`@media (prefers-color-scheme: dark)`. `body` sets an explicit background.
Never hard-code a hex value in a rule — reference a token.

| Token             | Role                                              |
|-------------------|---------------------------------------------------|
| `--bg`            | page surface (the only "panel" — there are none)  |
| `--bg-subtle`     | input fields, row hover, chips                    |
| `--text`          | primary text                                      |
| `--muted`         | secondary text, labels, counts, hints             |
| `--border`        | hairline dividers between rows                     |
| `--border-strong` | section rules, input borders                      |
| `--accent`        | primary action fill, focus ring                   |
| `--accent-text`   | text on `--accent`                                |
| `--accent-hover`  | primary action hover (a flat darker/lighter tone) |
| `--ok`            | approve affordance                                |
| `--danger`        | deny / revoke affordance                          |

Contrast: body text ≥ 4.5:1 against `--bg`; muted text ≥ 4.5:1 for anything a
user must read. Accent fills carry `--accent-text` at ≥ 4.5:1.

## Typography & spacing

- System UI font stack; monospace (`ui-monospace, Menlo, …`) only for
  machine values: scope strings, Client IDs, ports.
- Sizes: page title ~1.05rem; section label ~0.72rem uppercase; body ~0.9rem;
  hints/counts ~0.8rem. Line-height ~1.45 for prose.
- Spacing on a 4px scale (4/8/12/16/20/24). Rows ~0.6rem vertical padding;
  sections ~1.5rem apart.

## Components

- **Buttons** — flat, no gradient, radius ≤ 6px (a control, not a card).
  Primary = solid `--accent` fill + `--accent-text`. Secondary = transparent
  with a `--border-strong` outline. Semantic actions color by intent: Approve
  uses `--ok`, Deny/Revoke use `--danger` (outline by default, fill on hover
  is optional). Disabled = reduced opacity, no pointer.
- **Inputs** — `--bg-subtle` fill, `--border-strong` hairline, radius ≤ 6px.
- **Tags/chips** (scope names) — inline, `--bg-subtle` fill, small radius,
  monospace, muted. Labels, not containers — permitted.
- **Status / counts / hints** — plain muted text; no badge pills with accent
  fills.
- **Toast** — bottom-center, solid inverted fill (`--text` bg / `--bg` text),
  hairline border, no shadow; auto-dismisses.

## Accessibility & behavior

- Keyboard focus is always visible: a 2px `--accent` `outline` with offset on
  focus-visible for every interactive element.
- Respect `prefers-color-scheme` for light/dark; no manual theme toggle.
- Untrusted strings (character names, application names) are rendered with
  `textContent` only — never interpolated as HTML.
