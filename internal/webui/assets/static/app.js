"use strict";

// Session token injected into the page by the server. Sent on every /manage
// call; without it (or from a non-loopback origin) the server refuses.
const TOKEN = document.querySelector('meta[name="session-token"]').content;

const REFRESH_MS = 3000;
const SECTIONS = ["characters", "applications", "requests", "settings"];
const DEFAULT_SECTION = "characters";

const PERMISSION_LABELS = {
  list_characters: "list all characters",
  list_accounts: "list accounts",
};
const permissionLabel = (p) => PERMISSION_LABELS[p] || p;

const viewEl = document.getElementById("view");
let lastState = null;
// suppressAutoRender is set while a self-managed view (the scope editor) is on
// screen, so the 3s poll doesn't wipe in-progress edits by re-rendering.
let suppressAutoRender = false;
// expandedRows holds the keys of collapsible rows the user has opened.
const expandedRows = new Set();

// --- /manage API ---

async function api(method, path, body) {
  const opts = { method, headers: { "X-Proxy-Session": TOKEN } };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const resp = await fetch(path, opts);
  if (!resp.ok) {
    const text = (await resp.text()).trim();
    throw new Error(text || `request failed (${resp.status})`);
  }
  if (resp.status === 204) return null;
  return resp.json();
}

let toastTimer;
function toast(message, isError) {
  const el = document.getElementById("toast");
  el.textContent = message;
  el.classList.toggle("error", !!isError);
  el.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => { el.hidden = true; }, 3500);
}

// confirmDialog shows a modal and resolves true (confirmed) or false
// (cancelled, Escape, or backdrop click). The overlay lives on <body>, so the
// 3s poll re-rendering #view underneath doesn't dismiss it.
function confirmDialog({ title, message, confirmLabel = "Confirm", danger = false }) {
  return new Promise((resolve) => {
    const overlay = el("div", "modal-overlay");
    const modal = el("div", "modal");
    modal.setAttribute("role", "dialog");
    modal.setAttribute("aria-modal", "true");
    modal.appendChild(el("h2", "modal-title", title));
    if (message) modal.appendChild(el("p", "modal-message", message));

    const actions = el("div", "modal-actions");
    const cancelBtn = el("button", "btn", "Cancel");
    const confirmBtn = el("button", "btn " + (danger ? "revoke" : "primary"), confirmLabel);
    actions.append(cancelBtn, confirmBtn);
    modal.appendChild(actions);
    overlay.appendChild(modal);

    function close(result) {
      document.removeEventListener("keydown", onKey);
      overlay.remove();
      resolve(result);
    }
    function onKey(e) {
      if (e.key === "Escape") close(false);
    }
    cancelBtn.addEventListener("click", () => close(false));
    confirmBtn.addEventListener("click", () => close(true));
    overlay.addEventListener("click", (e) => { if (e.target === overlay) close(false); });
    document.addEventListener("keydown", onKey);

    document.body.appendChild(overlay);
    confirmBtn.focus();
  });
}

// --- DOM helpers (textContent only — character/app names are untrusted) ---

function el(tag, className, text) {
  const n = document.createElement(tag);
  if (className) n.className = className;
  if (text !== undefined) n.textContent = text;
  return n;
}

function scopesEl(scopes) {
  const wrap = el("div", "scopes");
  (scopes || []).forEach((s) => wrap.appendChild(el("span", "scope", s)));
  return wrap;
}

// --- time formatting (token ages) ---

// humanizeDuration renders a number of seconds as a compact "3s"/"12m"/"5h 2m"/
// "4d 3h" string.
function humanizeDuration(seconds) {
  const s = Math.max(0, Math.round(seconds));
  if (s < 60) return `${s}s`;
  const min = Math.floor(s / 60);
  if (min < 60) return `${min}m`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return min % 60 ? `${hr}h ${min % 60}m` : `${hr}h`;
  const d = Math.floor(hr / 24);
  return hr % 24 ? `${d}d ${hr % 24}h` : `${d}d`;
}

// relativeFromNow turns an ISO timestamp into "12m ago" / "in 5m", or "unknown".
function relativeFromNow(iso) {
  if (!iso) return "unknown";
  const t = new Date(iso).getTime();
  if (isNaN(t)) return "unknown";
  const diff = t - Date.now();
  const human = humanizeDuration(Math.abs(diff) / 1000);
  return diff >= 0 ? `in ${human}` : `${human} ago`;
}

// expiryText phrases an access-token expiry relative to now.
function expiryText(iso) {
  if (!iso) return "unknown";
  const t = new Date(iso).getTime();
  if (isNaN(t)) return "unknown";
  return (t - Date.now() >= 0 ? "expires " : "expired ") + relativeFromNow(iso);
}

// absoluteTime is the full local timestamp, used as a hover title.
function absoluteTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  return isNaN(d.getTime()) ? "" : d.toLocaleString();
}

// row builds a flat list entry: an info block plus optional action controls.
// actions is an array of {label, cls, done, onClick} (a button that calls the
// API) or {label, cls, href} (a link that navigates).
//
// opts.expandKey makes the row collapsible: only the title shows until it is
// clicked, then the subtitle and scopes appear. Expanded keys live in
// expandedRows so a row stays open across the 3s poll re-render.
function row(title, subtitle, scopes, actions, opts) {
  const r = el("div", "row");
  const info = el("div", "info");
  const details = [];
  if (subtitle) details.push(el("div", "sub", subtitle));
  if (scopes && scopes.length) details.push(scopesEl(scopes));

  const key = opts && opts.expandKey;
  if (key) {
    const expanded = expandedRows.has(key);
    const toggle = el("button", "row-toggle");
    toggle.type = "button";
    toggle.setAttribute("aria-expanded", String(expanded));
    toggle.append(el("span", "toggle-icon", "›"), el("span", "title", title));
    const body = el("div", "row-details");
    body.append(...details);
    body.hidden = !expanded;
    toggle.addEventListener("click", () => {
      const open = body.hidden;
      body.hidden = !open;
      toggle.setAttribute("aria-expanded", String(open));
      if (open) expandedRows.add(key); else expandedRows.delete(key);
    });
    info.append(toggle, body);
  } else {
    info.appendChild(el("div", "title", title));
    info.append(...details);
  }
  r.appendChild(info);

  if (actions && actions.length) {
    const act = el("div", "actions");
    actions.forEach((a) => {
      if (a.href) {
        const link = el("a", "btn " + (a.cls || ""), a.label);
        link.href = a.href;
        act.appendChild(link);
        return;
      }
      const b = el("button", "btn " + a.cls, a.label);
      b.addEventListener("click", async () => {
        b.disabled = true;
        try {
          await a.onClick();
          toast(a.done || "Done");
          await refreshState();
        } catch (err) {
          b.disabled = false;
          toast(err.message, true);
        }
      });
      act.appendChild(b);
    });
    r.appendChild(act);
  }
  return r;
}

// linkRow is a whole-row navigation target (e.g. a character → its detail).
function linkRow(title, subtitle, href) {
  const a = el("a", "row row-link");
  a.href = href;
  const info = el("div", "info");
  info.appendChild(el("div", "title", title));
  if (subtitle) info.appendChild(el("div", "sub", subtitle));
  a.appendChild(info);
  a.appendChild(el("span", "chevron", "›"));
  return a;
}

// group builds a labelled set of rows, or an empty-state line. headerActions,
// if given, is a DOM node placed on the right of the group label.
function group(label, items, emptyText, build, headerActions) {
  const g = el("div", "group");
  const header = el("div", "group-label");
  header.appendChild(el("span", null, label));
  if (headerActions && items.length) {
    header.classList.add("has-actions");
    header.appendChild(headerActions);
  }
  g.appendChild(header);
  if (!items.length) {
    g.appendChild(el("div", "empty", emptyText));
    return g;
  }
  items.forEach((item) => g.appendChild(build(item)));
  return g;
}

function backLink(href, label) {
  const a = el("a", "back-link", "‹ " + label);
  a.href = href;
  return a;
}

function pageHeading(inner, title, subtitle) {
  inner.appendChild(el("h1", "page-title", title));
  if (subtitle) inner.appendChild(el("p", "page-sub", subtitle));
}

function grantsForCharacter(id) {
  return (lastState.active_grants || []).filter((g) => g.character_id === id);
}

const plural = (n, word) => `${n} ${word}${n === 1 ? "" : "s"}`;

// applications derives the list of applications that hold anything — a
// per-character grant or an app-wide permission — from the state. Sorted by
// name; each entry carries its grants and permissions.
function applications() {
  const byId = new Map();
  const entry = (id, name) => {
    if (!byId.has(id)) byId.set(id, { id, name, grants: [], permissions: [] });
    return byId.get(id);
  };
  (lastState.active_grants || []).forEach((g) => entry(g.application_id, g.application_name).grants.push(g));
  (lastState.app_permissions || []).forEach((p) => entry(p.application_id, p.application_name).permissions.push(p));
  return Array.from(byId.values()).sort((a, b) => a.name.localeCompare(b.name));
}

// postAll POSTs to each path in turn, continuing past failures, and reports
// how many failed and the last error. Used by the bulk actions.
async function postAll(paths) {
  let failed = 0;
  let lastErr;
  for (const path of paths) {
    try {
      await api("POST", path);
    } catch (err) {
      failed++;
      lastErr = err;
    }
  }
  return { failed, lastErr };
}

// bulkRevoke confirms, then revokes the given grants and app-wide
// permissions. Like the bulk request actions it acts on exactly the IDs on
// screen when the user clicked. Returns true when everything was revoked.
async function bulkRevoke({ title, message, confirmLabel, grants = [], permissions = [] }) {
  const ok = await confirmDialog({ title, message, confirmLabel, danger: true });
  if (!ok) return false;
  const paths = [
    ...grants.map((g) => `/manage/grants/${g.id}/revoke`),
    ...permissions.map((p) => `/manage/app-permissions/${p.id}/revoke`),
  ];
  suppressAutoRender = true;
  let result;
  try {
    result = await postAll(paths);
  } finally {
    suppressAutoRender = false;
  }
  if (result.failed) {
    toast(`Revoked ${paths.length - result.failed} of ${paths.length}; ${result.failed} failed: ${result.lastErr.message}`, true);
  } else {
    toast("Access revoked");
  }
  await refreshState();
  return !result.failed;
}

// dangerButton is a destructive button inside a danger-zone group; onClick
// runs with the button disabled and re-enables it afterwards.
function dangerButton(label, onClick) {
  const b = el("button", "btn revoke", label);
  b.addEventListener("click", async () => {
    b.disabled = true;
    try {
      await onClick();
    } catch (err) {
      toast(err.message, true);
    } finally {
      b.disabled = false;
    }
  });
  return b;
}

// --- top-level views ---

// searchQueries holds each search box's text by key. It lives outside the
// views so it survives the 3s poll re-render (renderView also restores
// focus/caret on the box).
const searchQueries = new Map();

// searchableGroup appends a live name filter and the group it filters to
// parent. items are filtered by nameOf(item) containing the query (case-
// insensitive); buildGroup(shown, emptyText) returns the group node. Only the
// group is swapped on each keystroke, so the input keeps focus while typing.
function searchableGroup(parent, { key, placeholder, items, nameOf, emptyText, buildGroup }) {
  const query = () => searchQueries.get(key) || "";

  const search = el("input", "search");
  search.type = "search";
  search.id = `search-${key}`;
  search.placeholder = placeholder;
  search.autocomplete = "off";
  search.spellcheck = false;
  search.setAttribute("aria-label", placeholder);
  search.value = query();

  const build = () => {
    const q = query().trim().toLowerCase();
    const shown = q ? items.filter((i) => (nameOf(i) || "").toLowerCase().includes(q)) : items;
    const empty = items.length && q ? `No characters match “${query().trim()}”.` : emptyText;
    return buildGroup(shown, empty);
  };

  let list = build();
  parent.append(search, list);
  search.addEventListener("input", () => {
    searchQueries.set(key, search.value);
    const next = build();
    list.replaceWith(next);
    list = next;
  });
  search.addEventListener("keydown", (e) => {
    if (e.key === "Escape" && search.value) {
      e.preventDefault();
      search.value = "";
      search.dispatchEvent(new Event("input"));
    }
  });
}

function viewCharacters() {
  const inner = el("div", "view-inner");
  pageHeading(inner, "Characters");
  searchableGroup(inner, {
    key: "characters",
    placeholder: "Search characters by name",
    items: lastState.characters || [],
    nameOf: (c) => c.name,
    emptyText: "No characters added yet — use Add Character.",
    buildGroup: (shown, empty) => group("On this proxy", shown, empty, (c) => {
      const n = grantsForCharacter(c.id).length;
      const sub = [c.account_name ? `Account: ${c.account_name}` : null,
        n ? `${plural(n, "application")} with access` : "No application access"]
        .filter(Boolean).join(" · ");
      return linkRow(c.name, sub, `#/characters/${c.id}`);
    }),
  });
  return inner;
}

// bulkRequestActions builds "Approve all" / "Deny all" buttons for a list of
// pending requests. It acts on exactly the IDs on screen at click time (not
// whatever is pending server-side by then), so a request that arrives after
// the user looked is never approved sight-unseen. basePath is e.g.
// "/manage/pending-grants"; noun is the plural used in messages.
function bulkRequestActions(items, basePath, noun) {
  const wrap = el("div", "group-actions");
  const approveBtn = el("button", "btn approve", "Approve all");
  const denyBtn = el("button", "btn deny", "Deny all");
  wrap.append(approveBtn, denyBtn);

  const ids = items.map((p) => p.id);
  const count = `${ids.length} ${noun}`;

  async function runAll(action, doneVerb) {
    approveBtn.disabled = denyBtn.disabled = true;
    // Pause re-rendering so the poll doesn't rebuild these buttons mid-run.
    suppressAutoRender = true;
    let failed, lastErr;
    try {
      ({ failed, lastErr } = await postAll(ids.map((id) => `${basePath}/${id}/${action}`)));
    } finally {
      suppressAutoRender = false;
    }
    if (failed) {
      toast(`${doneVerb} ${ids.length - failed} of ${ids.length}; ${failed} failed: ${lastErr.message}`, true);
    } else {
      toast(`${doneVerb} ${count}`);
    }
    await refreshState();
  }

  approveBtn.addEventListener("click", async () => {
    const ok = await confirmDialog({
      title: `Approve all ${count}?`,
      message: "Every request listed will be granted. Review them first if you're not sure where they came from.",
      confirmLabel: "Approve all",
    });
    if (ok) await runAll("approve", "Approved");
  });
  denyBtn.addEventListener("click", () => runAll("deny", "Denied"));
  return wrap;
}

function viewRequests() {
  const inner = el("div", "view-inner");
  pageHeading(inner, "Pending Requests");
  const grants = lastState.pending_grants || [];
  inner.appendChild(group("Access requests", grants, "No pending access requests", (p) =>
    row(`${p.application_name} → ${p.character_name}`, "wants access to this character", p.scopes, [
      { label: "Approve", cls: "approve", done: "Approved", onClick: () => api("POST", `/manage/pending-grants/${p.id}/approve`) },
      { label: "Deny", cls: "deny", done: "Denied", onClick: () => api("POST", `/manage/pending-grants/${p.id}/deny`) },
    ]),
    bulkRequestActions(grants, "/manage/pending-grants", "access requests")
  ));
  const perms = lastState.pending_permissions || [];
  inner.appendChild(group("Permission requests", perms, "No pending permission requests", (p) =>
    row(p.application_name, `wants to ${permissionLabel(p.permission)}`, null, [
      { label: "Approve", cls: "approve", done: "Approved", onClick: () => api("POST", `/manage/pending-permissions/${p.id}/approve`) },
      { label: "Deny", cls: "deny", done: "Denied", onClick: () => api("POST", `/manage/pending-permissions/${p.id}/deny`) },
    ]),
    bulkRequestActions(perms, "/manage/pending-permissions", "permission requests")
  ));
  return inner;
}

function viewApplications() {
  const inner = el("div", "view-inner");
  pageHeading(inner, "Applications");
  inner.appendChild(group("With permissions", applications(), "No application holds any permissions.", (app) => {
    const parts = [];
    if (app.grants.length) parts.push(`${plural(app.grants.length, "character")} authorized`);
    if (app.permissions.length) parts.push(plural(app.permissions.length, "app-wide permission"));
    return linkRow(app.name, parts.join(" · "), `#/applications/${app.id}`);
  }));
  return inner;
}

// --- application detail: characters an app may use, and its app-wide permissions ---

function renderApplicationDetail(id) {
  const inner = el("div", "view-inner");
  inner.appendChild(backLink("#/applications", "Applications"));

  const app = applications().find((a) => a.id === id);
  if (!app) {
    pageHeading(inner, "Application");
    inner.appendChild(el("div", "empty", "This application no longer holds any permissions."));
    viewEl.replaceChildren(inner);
    return;
  }

  pageHeading(inner, app.name);

  // Search only when there's a list to search.
  const buildAuthorized = (shown, empty) => group("Authorized characters", shown, empty, (g) => {
    const n = g.scopes ? g.scopes.length : 0;
    return row(g.character_name, `${plural(n, "permission")} granted`, g.scopes, [
      { label: "Edit permissions", cls: "", href: `#/applications/${id}/grants/${g.id}` },
      { label: "Revoke", cls: "revoke", done: "Access revoked", onClick: () => api("POST", `/manage/grants/${g.id}/revoke`) },
    ]);
  });
  if (app.grants.length) {
    searchableGroup(inner, {
      key: `app-${id}-characters`,
      placeholder: "Search characters by name",
      items: app.grants,
      nameOf: (g) => g.character_name,
      emptyText: "Not authorized for any character.",
      buildGroup: buildAuthorized,
    });
  } else {
    inner.appendChild(buildAuthorized([], "Not authorized for any character."));
  }

  inner.appendChild(group("App-wide permissions", app.permissions, "No app-wide permissions.", (p) =>
    row(permissionLabel(p.permission), null, null, [
      { label: "Revoke", cls: "revoke", done: "Revoked", onClick: () => api("POST", `/manage/app-permissions/${p.id}/revoke`) },
    ])
  ));

  const dz = el("div", "group danger-zone");
  dz.appendChild(el("div", "group-label", "Danger zone"));
  dz.appendChild(el("p", "hint",
    "Revoking everything removes this application's access to every character and all of its app-wide permissions. It would have to request access again."));
  const actions = el("div", "token-actions");
  actions.appendChild(dangerButton("Revoke all permissions", async () => {
    const done = await bulkRevoke({
      title: "Revoke all permissions?",
      message: `${app.name} will lose access to ${plural(app.grants.length, "character")} and ${plural(app.permissions.length, "app-wide permission")}.`,
      confirmLabel: "Revoke all",
      grants: app.grants,
      permissions: app.permissions,
    });
    if (done) location.hash = "#/applications";
  }));
  dz.appendChild(actions);
  inner.appendChild(dz);

  viewEl.replaceChildren(inner);
}

function viewSettings() {
  const inner = el("div", "view-inner");
  pageHeading(inner, "Settings");
  const s = lastState;

  // EVE SSO Client ID: the bundled application by default, or the user's own.
  const clientBox = el("div", "setting");
  const customLabel = el("label", "checkbox");
  const custom = el("input");
  custom.type = "checkbox";
  custom.checked = !!s.use_custom_client_id;
  // Builds without a bundled Client ID can only use a custom one.
  custom.disabled = !s.bundled_client_id;
  customLabel.append(custom, " Use a custom EVE SSO Client ID");
  clientBox.appendChild(customLabel);
  clientBox.appendChild(el("p", "hint", s.bundled_client_id
    ? "By default esi-proxy logs characters in with its own built-in EVE application. Turn this on to use an application you registered yourself."
    : "This build has no built-in EVE application, so you need to register your own."));

  const customFields = el("div", "custom-client-id");
  customFields.hidden = !custom.checked;

  const steps = el("ol", "hint steps");
  const step1 = el("li");
  step1.append("Create an application at ");
  const link = el("a", null, "developers.eveonline.com");
  link.href = "https://developers.eveonline.com";
  link.target = "_blank";
  link.rel = "noopener";
  step1.append(link, ".");
  const step2 = el("li");
  step2.append("Set the connection type to ", el("strong", null, "Authentication & API Access"),
    " and pick the ESI scopes you want characters to be able to grant.");
  const step3 = el("li");
  step3.append("Set the callback URL to exactly:");
  const cbRow = el("div", "input-row");
  const cbInput = el("input");
  cbInput.type = "text";
  cbInput.readOnly = true;
  cbInput.value = s.callback_url || "";
  cbInput.addEventListener("focus", () => cbInput.select());
  const copyBtn = el("button", "btn", "Copy");
  copyBtn.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(cbInput.value);
      toast("Callback URL copied");
    } catch {
      cbInput.focus();
      toast("Couldn't copy — select the URL and copy it manually", true);
    }
  });
  cbRow.append(cbInput, copyBtn);
  step3.appendChild(cbRow);
  const step4 = el("li", null, "Paste the application's Client ID below. Saved immediately — no restart needed.");
  steps.append(step1, step2, step3, step4);
  customFields.appendChild(steps);

  const inputRow = el("div", "input-row");
  const input = el("input");
  input.type = "text";
  input.spellcheck = false;
  input.autocomplete = "off";
  input.placeholder = "paste Client ID";
  input.value = s.custom_client_id || "";
  const saveBtn = el("button", "btn", "Save");
  inputRow.append(input, saveBtn);
  customFields.appendChild(inputRow);
  if (!s.client_id_set) {
    customFields.appendChild(el("p", "hint", "No Client ID set yet — add one to log in characters."));
  }
  customFields.appendChild(el("p", "hint",
    "Characters added with one Client ID can't refresh their tokens under another. After switching, re-add them."));
  clientBox.appendChild(customFields);
  inner.appendChild(clientBox);

  custom.addEventListener("change", async () => {
    const enabled = custom.checked;
    customFields.hidden = !enabled;
    try {
      await api("POST", "/manage/custom-client-id", { enabled });
      toast(enabled ? "Using custom Client ID" : "Using built-in Client ID");
      await refreshState();
    } catch (err) {
      custom.checked = !enabled;
      customFields.hidden = !custom.checked;
      toast(err.message, true);
    }
  });

  saveBtn.addEventListener("click", async () => {
    const clientID = input.value.trim();
    if (!clientID) { toast("Enter a Client ID first", true); return; }
    try {
      await api("POST", "/manage/client-id", { client_id: clientID });
      toast("Client ID saved");
      await refreshState();
    } catch (err) {
      toast(err.message, true);
    }
  });

  // LAN access
  const lanBox = el("div", "setting");
  const lanLabel = el("label", "checkbox");
  const lan = el("input");
  lan.type = "checkbox";
  lan.checked = !!s.lan_access;
  lanLabel.append(lan, " Allow LAN access");
  lanBox.appendChild(lanLabel);
  lanBox.appendChild(el("p", "hint",
    "Let other machines on your network connect to this proxy. Takes effect immediately. Only enable on networks you trust."));
  inner.appendChild(lanBox);

  lan.addEventListener("change", async () => {
    const enabled = lan.checked;
    try {
      const res = await api("POST", "/manage/bind-host", { lan: enabled });
      const suffix = res && res.live ? "" : " — restart to apply";
      toast((enabled ? "LAN access on" : "LAN access off") + suffix);
    } catch (err) {
      lan.checked = !enabled;
      toast(err.message, true);
    }
  });

  // Developer options: only in development builds (the default;
  // release builds use -tags release). The server
  // doesn't even route these endpoints in release builds.
  if (s.dev_build) inner.appendChild(devSettings());

  return inner;
}

function devSettings() {
  const box = el("div", "dev-settings");
  box.appendChild(el("h2", "dev-settings-title", "Developer"));
  box.appendChild(el("p", "hint", "These options appear only in development builds."));

  const notifyBox = el("div", "setting");
  notifyBox.appendChild(el("label", "setting-label", "Desktop notifications"));
  notifyBox.appendChild(el("p", "hint",
    "Send a sample OS notification to check that notifications show up on this machine."));
  const notifyBtn = el("button", "btn", "Send test notification");
  notifyBox.appendChild(notifyBtn);
  box.appendChild(notifyBox);

  notifyBtn.addEventListener("click", async () => {
    notifyBtn.disabled = true;
    try {
      await api("POST", "/manage/dev/test-notification");
      toast("Test notification sent");
    } catch (err) {
      toast(err.message, true);
    } finally {
      notifyBtn.disabled = false;
    }
  });

  return box;
}

const TOP_VIEWS = {
  characters: viewCharacters,
  applications: viewApplications,
  requests: viewRequests,
  settings: viewSettings,
};

// --- character detail: applications with access to one character ---

// tokenStat is one labelled line in the token panel: a label, a relative-age
// value, and the absolute timestamp as a hover title / secondary line.
function tokenStat(label, valueText, iso, valueClass) {
  const stat = el("div", "token-stat");
  stat.appendChild(el("span", "token-stat-label", label));
  const value = el("span", "token-stat-value" + (valueClass ? " " + valueClass : ""), valueText);
  const abs = absoluteTime(iso);
  if (abs) value.title = abs;
  stat.appendChild(value);
  if (abs) stat.appendChild(el("span", "token-stat-abs", abs));
  return stat;
}

// tokenSection shows the age of a character's access/refresh tokens and a
// button to force an immediate refresh.
function tokenSection(character) {
  const g = el("div", "group");
  g.appendChild(el("div", "group-label", "Tokens"));

  const panel = el("div", "token-info");
  const expired = character.access_token_expires_at &&
    new Date(character.access_token_expires_at).getTime() <= Date.now();
  panel.appendChild(tokenStat("Access token", expiryText(character.access_token_expires_at),
    character.access_token_expires_at, expired ? "danger" : null));
  panel.appendChild(tokenStat("Tokens last refreshed", relativeFromNow(character.tokens_updated_at),
    character.tokens_updated_at));
  panel.appendChild(tokenStat("Character added", relativeFromNow(character.created_at),
    character.created_at));
  g.appendChild(panel);

  const actions = el("div", "token-actions");
  const refresh = el("button", "btn", "Refresh tokens");
  refresh.addEventListener("click", async () => {
    refresh.disabled = true;
    refresh.textContent = "Refreshing…";
    try {
      await api("POST", `/manage/characters/${character.id}/refresh`);
      toast("Tokens refreshed");
      await refreshState();
    } catch (err) {
      refresh.disabled = false;
      refresh.textContent = "Refresh tokens";
      toast(err.message, true);
    }
  });
  actions.appendChild(refresh);
  g.appendChild(actions);
  return g;
}

function renderCharacterDetail(id) {
  const inner = el("div", "view-inner");
  inner.appendChild(backLink("#/characters", "Characters"));

  const character = (lastState.characters || []).find((c) => c.id === id);
  if (!character) {
    pageHeading(inner, "Character");
    inner.appendChild(el("div", "empty", "This character is no longer on the proxy."));
    viewEl.replaceChildren(inner);
    return;
  }

  pageHeading(inner, character.name, character.account_name ? `Account: ${character.account_name}` : null);

  inner.appendChild(tokenSection(character));

  const grants = grantsForCharacter(id);
  inner.appendChild(group("Applications with access", grants, "No applications have access to this character.", (g) => {
    const n = g.scopes ? g.scopes.length : 0;
    return row(g.application_name, `${plural(n, "permission")} granted`, g.scopes, [
      { label: "Edit permissions", cls: "", href: `#/characters/${id}/grants/${g.id}` },
      { label: "Revoke", cls: "revoke", done: "Access revoked", onClick: () => api("POST", `/manage/grants/${g.id}/revoke`) },
    ], { expandKey: `grant-${g.id}` });
  }));

  inner.appendChild(dangerZone(character, grants));

  viewEl.replaceChildren(inner);
}

// dangerZone holds the character-level destructive actions, each behind a
// confirmation: revoking every application's access (keeping the character),
// and removing the character (and its tokens/grants) from the proxy.
function dangerZone(character, grants) {
  const g = el("div", "group danger-zone");
  g.appendChild(el("div", "group-label", "Danger zone"));
  g.appendChild(el("p", "hint",
    "Revoking all applications keeps the character but removes every application's access to it. Deleting the character also removes its stored tokens; to use it again you'd log it in from scratch."));

  const actions = el("div", "token-actions danger-actions");
  if (grants.length) {
    actions.appendChild(dangerButton("Revoke all applications", () => bulkRevoke({
      title: "Revoke all applications?",
      message: `${plural(grants.length, "application")} will lose access to ${character.name}. The character stays on the proxy.`,
      confirmLabel: "Revoke all",
      grants,
    })));
  }
  actions.appendChild(dangerButton("Delete character", async () => {
    const ok = await confirmDialog({
      title: "Delete character?",
      message: `${character.name} will be removed from the proxy, along with its stored tokens and every application's access to it. This can't be undone.`,
      confirmLabel: "Delete character",
      danger: true,
    });
    if (!ok) return;
    await api("DELETE", `/manage/characters/${character.id}`);
    toast("Character deleted");
    location.hash = "#/characters";
    await refreshState();
  }));
  g.appendChild(actions);
  return g;
}

// --- scope editor: update the specific permissions a grant holds ---

// renderGrantScopes is reachable from a character (#/characters/{id}/grants/
// {grantId}) or an application (#/applications/{id}/grants/{grantId});
// parent says which, and is where Back/Save/Revoke return to.
async function renderGrantScopes(parent, grantId) {
  const hashAtStart = location.hash;
  const grant = (lastState.active_grants || []).find((g) => g.id === grantId &&
    (parent.kind === "characters" ? g.character_id : g.application_id) === parent.id);
  const backHref = `#/${parent.kind}/${parent.id}`;
  const charId = grant ? grant.character_id : null;

  const inner = el("div", "view-inner");
  inner.appendChild(backLink(backHref, parent.kind === "characters" ? "Back to character" : "Back to application"));

  if (!grant) {
    pageHeading(inner, "Edit permissions");
    inner.appendChild(el("div", "empty", "This grant no longer exists."));
    viewEl.replaceChildren(inner);
    return;
  }

  pageHeading(inner, "Edit permissions", `${grant.application_name} → ${grant.character_name}`);
  inner.appendChild(el("p", "hint",
    "Tick the ESI permissions this application may use for this character. Unticking narrows its access; to remove it entirely, use Revoke."));

  // Fetch the character's available scopes (what its token can serve). The
  // editable universe is that set unioned with whatever the grant already has.
  const loading = el("div", "empty", "Loading permissions…");
  inner.appendChild(loading);
  viewEl.replaceChildren(inner);

  let available = [];
  try {
    const detail = await api("GET", `/manage/characters/${charId}`);
    available = detail.available_scopes || [];
  } catch (err) {
    toast(err.message, true);
  }
  if (location.hash !== hashAtStart) return; // user navigated away mid-fetch

  const universe = Array.from(new Set([...(available || []), ...(grant.scopes || [])])).sort();
  const granted = new Set(grant.scopes || []);

  loading.remove();

  const toolbar = el("div", "editor-toolbar");
  const selectAll = el("button", "btn", "Select all");
  const clearAll = el("button", "btn", "Clear");
  toolbar.append(selectAll, clearAll);
  inner.appendChild(toolbar);

  const list = el("div", "scope-list");
  const boxes = universe.map((scope) => {
    const label = el("label", "scope-check");
    const cb = el("input");
    cb.type = "checkbox";
    cb.value = scope;
    cb.checked = granted.has(scope);
    label.append(cb, el("span", "scope-name", scope));
    list.appendChild(label);
    return cb;
  });
  inner.appendChild(list);
  selectAll.addEventListener("click", () => boxes.forEach((b) => (b.checked = true)));
  clearAll.addEventListener("click", () => boxes.forEach((b) => (b.checked = false)));

  const footer = el("div", "editor-footer");
  const save = el("button", "btn primary", "Save permissions");
  const cancel = el("a", "btn", "Cancel");
  cancel.href = backHref;
  const revoke = el("button", "btn revoke", "Revoke access");
  footer.append(save, cancel, revoke);
  inner.appendChild(footer);

  save.addEventListener("click", async () => {
    const scopes = boxes.filter((b) => b.checked).map((b) => b.value);
    if (!scopes.length) {
      toast("Select at least one permission, or use Revoke access", true);
      return;
    }
    save.disabled = true;
    try {
      await api("POST", `/manage/grants/${grantId}/scopes`, { scopes });
      toast("Permissions updated");
      await refreshState();
      location.hash = backHref;
    } catch (err) {
      save.disabled = false;
      toast(err.message, true);
    }
  });

  revoke.addEventListener("click", async () => {
    revoke.disabled = true;
    try {
      await api("POST", `/manage/grants/${grantId}/revoke`);
      toast("Access revoked");
      await refreshState();
      location.hash = backHref;
    } catch (err) {
      revoke.disabled = false;
      toast(err.message, true);
    }
  });

  viewEl.replaceChildren(inner);
}

// --- routing & chrome ---

function parseRoute() {
  const parts = location.hash.replace(/^#\/?/, "").split("/").filter(Boolean);
  const section = SECTIONS.includes(parts[0]) ? parts[0] : DEFAULT_SECTION;
  return { section, parts };
}

// renderView re-renders the current route. Re-rendering replaces the DOM, so
// a focused input that carries an id (e.g. a search box) gets its focus and
// caret back after the swap.
function renderView() {
  if (!lastState) return;
  const active = document.activeElement;
  const keep = active && active.id && viewEl.contains(active)
    ? { id: active.id, start: active.selectionStart, end: active.selectionEnd } : null;

  renderRoute();

  if (keep) {
    const next = document.getElementById(keep.id);
    if (next && next !== document.activeElement) {
      next.focus();
      try { next.setSelectionRange(keep.start, keep.end); } catch (_) { /* not a text input */ }
    }
  }
}

function renderRoute() {
  const { section, parts } = parseRoute();
  suppressAutoRender = false;

  const nested = section === "characters" || section === "applications";
  if (nested && parts.length >= 4 && parts[2] === "grants") {
    suppressAutoRender = true; // scope editor manages its own state
    renderGrantScopes({ kind: section, id: Number(parts[1]) }, Number(parts[3]));
    return;
  }
  if (section === "characters" && parts.length >= 2) {
    renderCharacterDetail(Number(parts[1]));
    return;
  }
  if (section === "applications" && parts.length >= 2) {
    renderApplicationDetail(Number(parts[1]));
    return;
  }
  viewEl.replaceChildren(TOP_VIEWS[section]());
}

function updateChrome(s) {
  const status = document.getElementById("status");
  status.textContent = `Running on ${location.host}`;
  status.classList.remove("offline");

  const { section } = parseRoute();
  document.querySelectorAll(".nav-item").forEach((a) => {
    a.classList.toggle("active", a.dataset.nav === section);
  });
  const pending = (s.pending_grants || []).length + (s.pending_permissions || []).length;
  document.querySelector('[data-nav-count="requests"]').textContent = pending ? String(pending) : "";
}

function setOffline() {
  const status = document.getElementById("status");
  status.textContent = "Not responding";
  status.classList.add("offline");
}

// refreshState polls the server and re-renders. It leaves the view alone while
// the scope editor is up, or while the user is editing the Settings form, so
// in-progress input isn't wiped.
async function refreshState() {
  try {
    const s = await api("GET", "/manage/state");
    lastState = s;
    updateChrome(s);
    const editingSettings = parseRoute().section === "settings" &&
      document.activeElement && viewEl.contains(document.activeElement);
    if (!suppressAutoRender && !editingSettings) renderView();
  } catch (err) {
    setOffline();
  }
}

// --- static wiring ---

document.getElementById("add-character").addEventListener("click", async () => {
  try {
    const { login_url } = await api("POST", "/manage/characters/add");
    // SSO must complete in the user's real browser. In the native app window
    // the webview exposes openExternal for that; in a plain browser tab, open
    // a new tab rather than navigating this page.
    if (typeof window.openExternal === "function") {
      await window.openExternal(login_url);
    } else {
      window.open(login_url, "_blank", "noopener");
    }
    toast("Opening EVE SSO login…");
  } catch (err) {
    toast(err.message, true);
  }
});

window.addEventListener("hashchange", () => {
  if (lastState) updateChrome(lastState);
  renderView();
});

if (!location.hash) location.replace("#/" + DEFAULT_SECTION);

refreshState();
setInterval(refreshState, REFRESH_MS);
