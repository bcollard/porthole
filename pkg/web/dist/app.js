import { Terminal } from "https://cdn.jsdelivr.net/npm/@xterm/xterm@5.5.0/+esm";
import { FitAddon } from "https://cdn.jsdelivr.net/npm/@xterm/addon-fit@0.10.0/+esm";
import { WebLinksAddon } from "https://cdn.jsdelivr.net/npm/@xterm/addon-web-links@0.11.0/+esm";

// Public base path. The SPA is always served at "<basePath>/ui/", so
// the prefix is whatever sits in front of "/ui/" in the page URL. This
// lets a single build serve at "/" (no gateway prefix) or under any
// sub-path (e.g. "/porthole" when fronted by a shared API gateway).
const BASE_PATH = (() => {
  const m = window.location.pathname.match(/^(.*)\/ui\/?/);
  return m ? m[1] : "";
})();

const state = {
  config: null,
  namespaces: [],
  pods: [],
  ecs: [],
  sessions: [],
  selectedNs: null,
  selectedPod: null,
  // selectedEc is a denormalized view of state.activeKey's ec part,
  // used by renderECBar / updateTargetText. Updated on switch.
  selectedEc: null,
  // Multi-session terminal state. Each entry:
  //   { ns, pod, ec, key, ws, term, fit, container, dead }
  // Keyed by `${ns}/${pod}/${ec}`. One entry per attached session in
  // this browser tab; only one is visible at a time (activeKey).
  attached: new Map(),
  activeKey: null,
  // Set<sessionKey> of ECs we've asked the server to terminate. Lets
  // the EC bar + sessions dropdown hide them across refreshes /
  // navigation — `pendingTerminate` alone is wiped whenever state.ecs
  // is replaced, and kubelet often takes 1-3s (sometimes longer) to
  // stamp Terminated=true after the process exits. Entries are
  // dropped when the server confirms Terminated=true, or after 30s
  // as a safety net so a stuck kubelet doesn't hide a chip forever.
  terminating: new Set(),
};

function sessionKey(ns, pod, ec) {
  return `${ns}/${pod}/${ec}`;
}

function markTerminating(ns, pod, ecName) {
  const key = sessionKey(ns, pod, ecName);
  state.terminating.add(key);
  setTimeout(() => {
    if (state.terminating.delete(key)) {
      renderECBar();
      renderSessions();
    }
  }, 30_000);
}

const $ = (id) => document.getElementById(id);

// ---------- status & toast ----------
function setStatus(kind, text) {
  const el = $("status");
  el.className = "status " + kind;
  $("status-text").textContent = text;
}

let toastTimer = null;
function toast(message, kind = "info") {
  let el = document.querySelector(".toast");
  if (!el) {
    el = document.createElement("div");
    el.className = "toast";
    document.body.appendChild(el);
  }
  el.textContent = message;
  el.className = "toast " + kind + " show";
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => {
    el.className = "toast " + kind;
  }, 3500);
}

// ---------- terminal ----------
const TERM_OPTIONS = {
  fontFamily: 'ui-monospace, "SF Mono", Menlo, Consolas, monospace',
  fontSize: 13,
  lineHeight: 1.2,
  cursorBlink: true,
  cursorStyle: "bar",
  allowProposedApi: true,
  theme: {
    background: "#000000",
    foreground: "#e6e8ec",
    cursor: "#6ea8fe",
    cursorAccent: "#000000",
    selectionBackground: "rgba(110,168,254,0.3)",
    black: "#1a1f29",
    red: "#f87171",
    green: "#4ade80",
    yellow: "#fbbf24",
    blue: "#6ea8fe",
    magenta: "#c084fc",
    cyan: "#22d3ee",
    white: "#e6e8ec",
    brightBlack: "#475569",
    brightRed: "#fca5a5",
    brightGreen: "#86efac",
    brightYellow: "#fde68a",
    brightBlue: "#93c5fd",
    brightMagenta: "#d8b4fe",
    brightCyan: "#67e8f9",
    brightWhite: "#f8fafc",
  },
};

// initTerminal sets up the single ResizeObserver that refits whichever
// term-pane is currently visible. Individual xterm.js instances are
// created lazily by attachNewSession.
function initTerminal() {
  const ro = new ResizeObserver(() => {
    const rec = state.activeKey ? state.attached.get(state.activeKey) : null;
    if (rec) { try { rec.fit.fit(); } catch (_) {} }
  });
  ro.observe($("terminal"));
}

// ---------- HTTP helpers ----------
// When deployed behind Envoy Gateway, the gateway adds Authorization
// itself. For unit testing without the gateway you can poke a token
// into localStorage under key "porthole.token" and it'll be sent.
function authHeader() {
  const t = (typeof localStorage !== "undefined") && localStorage.getItem("porthole.token");
  return t ? { Authorization: "Bearer " + t } : {};
}

async function http(path, opts = {}) {
  const headers = Object.assign({}, opts.headers || {}, authHeader());
  const res = await fetch(BASE_PATH + path, { ...opts, headers });
  if (!res.ok) {
    const text = await res.text().catch(() => "");
    throw new Error(`${res.status} ${res.statusText}${text ? ": " + text : ""}`);
  }
  return res.json();
}

// ---------- data loaders ----------
async function loadConfig() {
  state.config = await http("/api/config");
  $("image-input").value = state.config.defaultImage || "";
  $("image-input").placeholder = state.config.defaultImage || "debug image";
}

// ---------- principal / logout ----------
// Best-guess display name from the JWT claims the server gave us.
// Keycloak's defaults: `name` (when first/last set), `given_name`,
// `preferred_username`, then the email local-part as a fallback.
function displayName(p) {
  return (
    p.given_name ||
    (p.name && p.name.split(" ")[0]) ||
    p.preferred_username ||
    (p.email && p.email.split("@")[0]) ||
    p.sub ||
    "unknown"
  );
}

// Format one OPA binding as the text of a role chip — "role: scope".
// Scope is the namespace_glob, the label set, or "*" for cluster-wide.
// Business-hours bindings get a clock affordance.
function bindingChipText(b) {
  const parts = [];
  if (b.namespace_glob) parts.push(b.namespace_glob);
  if (b.namespace_labels) {
    parts.push(
      Object.entries(b.namespace_labels)
        .map(([k, v]) => `${k}=${v}`)
        .join(","),
    );
  }
  if (parts.length === 0) parts.push("*");
  const scope = parts.join(" + ");
  const clock = b.business_hours ? " ⏰" : "";
  return `${b.role}: ${scope}${clock}`;
}

function bindingChipTooltip(b) {
  const lines = [`group: ${b.group}`, `role:  ${b.role}`];
  if (b.namespace_glob) lines.push(`scope: namespace ~ ${b.namespace_glob}`);
  if (b.namespace_labels) {
    lines.push(
      `scope: labels ${Object.entries(b.namespace_labels)
        .map(([k, v]) => `${k}=${v}`)
        .join(", ")}`,
    );
  }
  if (b.business_hours) lines.push("business hours only (09:00-17:00 UTC, Mon-Fri)");
  return lines.join("\n");
}

function renderRoleChips(bindings) {
  const wrap = $("user-roles");
  wrap.innerHTML = "";
  if (!bindings || bindings.length === 0) {
    wrap.hidden = true;
    return;
  }
  for (const b of bindings) {
    const chip = document.createElement("span");
    chip.className = `user-role role-${escapeHtml(b.role || "unknown")}`;
    chip.textContent = bindingChipText(b);
    chip.title = bindingChipTooltip(b);
    wrap.appendChild(chip);
  }
  wrap.hidden = false;
}

async function loadMe() {
  const userEl = $("user");
  const nameEl = $("user-name");
  const logoutEl = $("user-logout");
  try {
    const me = await http("/api/me");
    state.me = me;
    nameEl.textContent = displayName(me);
    nameEl.title = me.email || me.sub || "";
    renderRoleChips(me.bindings);
    // AUTH_DISABLED stamps a fixed local-dev principal; there's no
    // OIDC layer to log out from, so hide the link.
    if (me.sub === "local-dev") {
      logoutEl.hidden = true;
    } else {
      const suffix = (state.config && state.config.logoutPath) || "/logout";
      // href stays as a fallback target for "Open in new tab"
      // and for keyboard navigation. The click handler wires the
      // real two-step flow.
      logoutEl.href = BASE_PATH + suffix;
      logoutEl.addEventListener("click", doLogout);
    }
    userEl.hidden = false;
  } catch (e) {
    // /api/me requires auth; if it fails we just don't render the
    // user widget — the rest of the UI degrades cleanly.
    console.warn("loadMe:", e.message);
  }
}

// Two-step logout:
//
//   1. GET the gateway's logoutPath. EG's OIDC filter drops its
//      session cookie there but otherwise does nothing.
//   2. Navigate to the IdP's end-session endpoint with client_id
//      (from the JWT's azp claim) and post_logout_redirect_uri
//      pointing back at the SPA root. The IdP invalidates its SSO
//      session and redirects the browser back; EG then sees no
//      cookie and re-runs the OIDC handshake against a now-empty
//      IdP session, so the user gets a fresh login prompt instead
//      of being silently signed back in.
//
// Falls back to the single-step gateway-cookie clear when we don't
// know the IdP logout URL (server hasn't been configured, or
// AUTH_DISABLED-style local dev).
async function doLogout(e) {
  e.preventDefault();
  const cfg = state.config || {};
  const me = state.me || {};
  const logoutPath = cfg.logoutPath || "/logout";
  const idpLogout = cfg.idpLogoutURL;

  // 1. Drop EG's session cookie. credentials:"same-origin" is the
  // default for fetch; the cookie is sent, the response is dropped.
  try {
    await fetch(BASE_PATH + logoutPath, { credentials: "same-origin" });
  } catch (err) {
    console.warn("gateway logout fetch failed:", err);
  }

  // 2. Redirect to the IdP for the SSO-session invalidation.
  if (idpLogout && me.azp) {
    const postLogout = `${window.location.origin}${BASE_PATH}/`;
    const url =
      idpLogout +
      "?client_id=" + encodeURIComponent(me.azp) +
      "&post_logout_redirect_uri=" + encodeURIComponent(postLogout);
    window.location.href = url;
    return;
  }
  // Fallback: at least the local session is gone. Reload so the
  // gateway re-prompts (and the SPA reflects the new state).
  window.location.href = BASE_PATH + "/";
}

async function loadNamespaces() {
  try {
    state.namespaces = await http("/explore");
    renderNamespaces();
  } catch (e) {
    $("ns-list").innerHTML = `<li class="empty">Error: ${escapeHtml(e.message)}</li>`;
    toast("Failed to load namespaces: " + e.message, "error");
  }
}

async function loadPods(ns) {
  $("pod-list").innerHTML = `<li class="empty">Loading…</li>`;
  $("pod-filter").disabled = true;
  try {
    state.pods = await http(`/explore/ns/${encodeURIComponent(ns)}`);
    $("pod-filter").disabled = false;
    renderPods();
  } catch (e) {
    $("pod-list").innerHTML = `<li class="empty">Error: ${escapeHtml(e.message)}</li>`;
    toast("Failed to load pods: " + e.message, "error");
  }
}

async function loadEphemeralContainers(ns, pod) {
  try {
    const r = await http(`/explore/ns/${encodeURIComponent(ns)}/pods/${encodeURIComponent(pod)}/ec`);
    state.ecs = r.ephemeralContainers || [];
    // Server has caught up on anything kubelet has stamped
    // Terminated=true — drop those from the local "we asked to
    // terminate" Set so we don't keep filtering them out of future
    // refreshes for no reason.
    for (const ec of state.ecs) {
      if (ec.Terminated) {
        state.terminating.delete(sessionKey(ns, pod, ec.Name));
      }
    }
    renderECBar();
  } catch (e) {
    state.ecs = [];
    renderECBar();
    toast("Failed to list ephemeral containers: " + e.message, "error");
  }
}

async function loadServices(ns) {
  const ul = $("svc-list");
  ul.innerHTML = `<li class="empty">Loading…</li>`;
  try {
    const r = await http(`/explore/ns/${encodeURIComponent(ns)}/svc`);
    state.services = r.services || [];
    renderServices();
  } catch (e) {
    state.services = [];
    ul.innerHTML = `<li class="empty">Error: ${escapeHtml(e.message)}</li>`;
  }
}

function renderServices() {
  const ul = $("svc-list");
  ul.innerHTML = "";
  if (!state.selectedNs) {
    ul.innerHTML = `<li class="empty">Select a namespace.</li>`;
    return;
  }
  if (!state.services || state.services.length === 0) {
    ul.innerHTML = `<li class="empty">No services in this namespace.</li>`;
    return;
  }
  for (const s of state.services) {
    const li = document.createElement("li");
    li.className = "svc-item";
    const portList = (s.ports || [])
      .map((p) => (p.name ? `${escapeHtml(p.name)}:${p.port}` : String(p.port)))
      .join(", ");
    li.innerHTML =
      `<span class="svc-name">${escapeHtml(s.name)}</span>` +
      `<span class="svc-ports">${portList || "<no ports>"}</span>`;
    // Click to copy a curl line a debug container can paste verbatim.
    // Inside the same ns, the short DNS name (svc:port) is enough.
    li.title = "Click to copy a curl command";
    li.addEventListener("click", () => copyCurlForService(s));
    ul.appendChild(li);
  }
}

function copyCurlForService(svc) {
  if (!svc.ports || svc.ports.length === 0) return;
  const p = svc.ports[0];
  const scheme = p.name === "https" || p.port === 443 ? "https" : "http";
  // Cross-namespace short form (<svc>.<ns>:port). Works from any pod
  // in the cluster — including a debug container the operator just
  // injected somewhere else.
  const ns = state.selectedNs;
  const host = ns ? `${svc.name}.${ns}` : svc.name;
  const cmd = `curl ${scheme}://${host}:${p.port}/`;
  navigator.clipboard.writeText(cmd).then(
    () => toast(`Copied: ${cmd}`, "success"),
    () => toast(`Couldn't copy — manual: ${cmd}`, "error"),
  );
}

async function loadPodDetail(ns, pod) {
  // Best-effort — failure just leaves the labels strip empty.
  try {
    const r = await http(`/explore/ns/${encodeURIComponent(ns)}/pods/${encodeURIComponent(pod)}`);
    state.podDetail = r;
  } catch (e) {
    state.podDetail = null;
  }
  renderTargetLabels();
}

function renderTargetLabels() {
  const bar = $("target-labels");
  if (!bar) return;
  bar.innerHTML = "";
  const labels = (state.podDetail && state.podDetail.labels) || {};
  const keys = Object.keys(labels).sort();
  if (!state.selectedPod || keys.length === 0) return;
  for (const k of keys) {
    const chip = document.createElement("span");
    chip.className = "target-label";
    chip.title = `${k}=${labels[k]}`;
    chip.innerHTML =
      `<span class="target-label-k">${escapeHtml(k)}</span>` +
      `<span class="target-label-eq">=</span>` +
      `<span class="target-label-v">${escapeHtml(labels[k])}</span>`;
    bar.appendChild(chip);
  }
}

// ---------- render ----------
function renderNamespaces() {
  const filter = $("ns-filter").value.toLowerCase();
  const filtered = state.namespaces.filter((n) => n.toLowerCase().includes(filter));
  const ul = $("ns-list");
  if (filtered.length === 0) {
    ul.innerHTML = `<li class="empty">No namespaces.</li>`;
    return;
  }
  ul.innerHTML = "";
  for (const ns of filtered) {
    const li = document.createElement("li");
    li.textContent = ns;
    li.title = ns;
    if (ns === state.selectedNs) li.classList.add("active");
    li.addEventListener("click", () => selectNamespace(ns));
    ul.appendChild(li);
  }
}

function renderPods() {
  const filter = $("pod-filter").value.toLowerCase();
  const filtered = state.pods.filter((p) => p.toLowerCase().includes(filter));
  const ul = $("pod-list");
  if (filtered.length === 0) {
    ul.innerHTML = `<li class="empty">No pods.</li>`;
    return;
  }
  ul.innerHTML = "";
  for (const pod of filtered) {
    const li = document.createElement("li");
    li.textContent = pod;
    li.title = pod;
    if (pod === state.selectedPod) li.classList.add("active");
    li.addEventListener("click", () => selectPod(pod));
    ul.appendChild(li);
  }
}

function renderECBar() {
  const bar = $("ec-bar");
  if (!state.selectedPod) {
    bar.innerHTML = "";
    return;
  }
  // Ephemeral containers are immutable in the pod spec — k8s won't
  // let us remove them — so a terminated EC lingers forever in the
  // spec. Hide everything that isn't actively running: most users
  // only want to see what's still alive.
  //
  // Why "Running" and not "not Terminated": kubelet takes ~1-3s to
  // stamp State.Terminated after PID 1 exits, so right after a
  // cleanup the EC briefly reports {Running:false, Terminated:false}.
  // Filtering on `!Terminated` left those pills stuck on screen.
  //
  // `pendingTerminate` is a client-only flag we set while a kill is
  // in flight; the pill stays visible (with a spinner) until the
  // next server reload either flips Terminated or drops Running.
  //
  // `state.terminating` is the longer-lived cousin: it survives the
  // state.ecs reload and the user navigating away and back, so a pill
  // can't pop back into view (and become clickable into a dead WS)
  // just because kubelet hasn't stamped Terminated=true yet.
  const visible = state.ecs.filter((ec) => {
    const k = sessionKey(state.selectedNs, state.selectedPod, ec.Name);
    if (state.terminating.has(k)) return false;
    return ec.Running || ec.pendingTerminate;
  });

  bar.innerHTML = "";
  if (visible.length === 0) {
    bar.appendChild(makeEmptyMessage());
  } else {
    for (const ec of visible) bar.appendChild(makeChip(ec));
  }

  // Right-aligned action group: refresh + (when applicable) cleanup.
  const actions = document.createElement("div");
  actions.className = "ec-actions";

  const refresh = document.createElement("button");
  refresh.className = "ec-action ec-refresh";
  refresh.title = "Refresh the ephemeral-container list";
  refresh.setAttribute("aria-label", "Refresh");
  refresh.innerHTML = `<span class="ec-refresh-icon">↻</span>`;
  refresh.addEventListener("click", () => refreshECs(refresh));
  actions.appendChild(refresh);

  const cleanable = visible.some(
    (e) => e.Running && !e.pendingTerminate && e.Name.startsWith("porthole-"),
  );
  if (cleanable) {
    const btn = document.createElement("button");
    btn.id = "cleanup-all-btn";
    btn.className = "ec-action ec-cleanup";
    btn.title = "Terminate every porthole-injected ephemeral container in this pod";
    btn.textContent = "Clean up all";
    btn.addEventListener("click", () => cleanupPod(btn));
    actions.appendChild(btn);
  }
  bar.appendChild(actions);
}

function makeEmptyMessage() {
  const el = document.createElement("span");
  el.className = "ec-empty";
  el.textContent = "No ephemeral containers. Inject one →";
  return el;
}

function makeChip(ec) {
  const chip = document.createElement("button");
  chip.className = "ec-chip";
  if (ec.Running) chip.classList.add("running");
  if (ec.pendingTerminate) chip.classList.add("terminating");
  // Highlight only when this chip's (ns, pod, ec) matches the active
  // session — otherwise a same-named chip in a different pod (rare
  // but possible) would falsely light up.
  const chipKey = sessionKey(state.selectedNs, state.selectedPod, ec.Name);
  if (state.activeKey === chipKey) chip.classList.add("active");
  chip.title = ec.pendingTerminate
    ? "Terminating…"
    : ec.Running
      ? "Click to attach"
      : "Container is not running";
  chip.innerHTML =
    `<span class="ec-dot"></span>` +
    `<span class="ec-label">${escapeHtml(ec.Name)}</span>`;

  if (ec.pendingTerminate) {
    // Replace the × with an inline spinner; chip is non-interactive
    // until the next reload confirms the termination.
    const sp = document.createElement("span");
    sp.className = "spinner ec-chip-spinner";
    chip.appendChild(sp);
    chip.disabled = true;
    return chip;
  }
  if (!ec.Running) {
    chip.disabled = true;
    return chip;
  }

  chip.addEventListener("click", () => attachToEc(ec.Name));
  // Per-EC × — only on porthole-* (the only names the server will
  // accept for the single-EC terminate endpoint).
  if (ec.Name.startsWith("porthole-")) {
    const x = document.createElement("span");
    x.className = "ec-close";
    x.setAttribute("role", "button");
    x.setAttribute("aria-label", "Terminate this ephemeral container");
    x.title = "Terminate this ephemeral container";
    x.textContent = "×";
    x.addEventListener("click", (e) => {
      e.stopPropagation();
      terminateOneEC(ec.Name);
    });
    chip.appendChild(x);
  }
  return chip;
}

async function refreshECs(btn) {
  if (!state.selectedNs || !state.selectedPod) return;
  if (btn) btn.classList.add("busy");
  try {
    await loadEphemeralContainers(state.selectedNs, state.selectedPod);
  } finally {
    if (btn) btn.classList.remove("busy");
  }
}

async function terminateOneEC(ecName) {
  const ns = state.selectedNs;
  const pod = state.selectedPod;
  const ec = state.ecs.find((e) => e.Name === ecName);
  if (!ec) return;

  // Flip into the in-flight state — chip re-renders with a spinner
  // and stays visible until the server reload removes it for real.
  ec.pendingTerminate = true;
  markTerminating(ns, pod, ecName);
  closeSessionByEc(ns, pod, ecName);
  renderECBar();
  renderSessions();

  try {
    await http(
      `/debug/cleanup/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}/${encodeURIComponent(ecName)}`,
      { method: "POST" },
    );
    await loadEphemeralContainers(ns, pod);
    loadSessions();
  } catch (e) {
    ec.pendingTerminate = false;
    state.terminating.delete(sessionKey(ns, pod, ecName));
    renderECBar();
    renderSessions();
    toast("Terminate failed: " + e.message, "error");
  }
}

async function cleanupPod(btn) {
  if (!state.selectedNs || !state.selectedPod) return;
  const ns = state.selectedNs;
  const pod = state.selectedPod;

  // Mark every porthole-* running EC pending, so each pill shows a
  // spinner alongside the button-level "Cleaning up…" indicator.
  // Also close any local sessions we hold to this pod's ECs.
  for (const ec of state.ecs) {
    if (ec.Running && ec.Name.startsWith("porthole-")) {
      ec.pendingTerminate = true;
      markTerminating(ns, pod, ec.Name);
      closeSessionByEc(ns, pod, ec.Name);
    }
  }
  if (btn) {
    btn.disabled = true;
    btn.classList.add("busy");
    btn.innerHTML = `<span class="spinner"></span><span>Cleaning up…</span>`;
  }
  renderECBar();

  try {
    const res = await http(
      `/debug/cleanup/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}`,
      { method: "POST" },
    );
    const ok = (res.results || []).filter((r) => r.ok).length;
    toast(`Terminated ${ok} ephemeral container${ok === 1 ? "" : "s"}`, "success");
  } catch (e) {
    for (const ec of state.ecs) ec.pendingTerminate = false;
    toast("Cleanup failed: " + e.message, "error");
  } finally {
    await loadEphemeralContainers(ns, pod);
    loadSessions();
  }
}

function updateTargetText() {
  const el = $("target-text");
  if (!state.selectedPod) {
    el.textContent = "no pod selected";
    return;
  }
  let s = `${state.selectedNs} :: ${state.selectedPod}`;
  if (state.selectedEc) s += ` :: ${state.selectedEc}`;
  el.textContent = s;
}

// ---------- selection handlers ----------
// selectNamespace and selectPod move the sidebar around — they no
// longer touch the attached-session map or the visible terminal.
// The terminal stays attached to whatever EC the user last clicked,
// regardless of where they're browsing. The EC chip for the active
// session is highlighted only when the user's currently looking at
// the same pod (renderECBar handles this).
function selectNamespace(ns) {
  if (state.selectedNs === ns) return;
  state.selectedNs = ns;
  state.selectedPod = null;
  state.pods = [];
  state.ecs = [];
  state.services = [];
  state.podDetail = null;
  $("ns-current").textContent = ns;
  $("ns-current-svc").textContent = ns;
  $("inject-btn").disabled = true;
  renderNamespaces();
  renderPods();
  renderServices();
  renderECBar();
  updateTargetText();
  renderTargetLabels();
  updateInstructions();
  loadPods(ns);
  loadServices(ns);
}

function selectPod(pod) {
  if (state.selectedPod === pod) return;
  state.selectedPod = pod;
  state.ecs = [];
  state.podDetail = null;
  $("inject-btn").disabled = false;
  renderPods();
  renderECBar();
  updateTargetText();
  renderTargetLabels();
  updateInstructions();
  loadEphemeralContainers(state.selectedNs, pod);
  loadPodDetail(state.selectedNs, pod);
}

// ---------- inject ----------
async function injectDebugger() {
  if (!state.selectedNs || !state.selectedPod) return;
  const btn = $("inject-btn");
  btn.disabled = true;
  const originalLabel = btn.innerHTML;
  btn.classList.add("busy");
  btn.innerHTML = `<span class="spinner"></span><span>Injecting…</span>`;
  try {
    const image = $("image-input").value.trim() || state.config.defaultImage;
    const body = { namespace: state.selectedNs, pod: state.selectedPod, image };
    const res = await http("/debug/inject", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    toast(`Injected ${res.debugContainerName}`, "success");
    loadSessions();
    // Poll briefly until the EC shows up as Running, then auto-attach.
    await pollAndAttach(state.selectedNs, state.selectedPod, res.debugContainerName);
  } catch (e) {
    toast("Inject failed: " + e.message, "error");
  } finally {
    btn.classList.remove("busy");
    btn.innerHTML = originalLabel;
    btn.disabled = false;
  }
}

async function pollAndAttach(ns, pod, ecName) {
  setStatus("connecting", `Waiting for ${ecName}…`);
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    await loadEphemeralContainers(ns, pod);
    const found = state.ecs.find((e) => e.Name === ecName);
    if (found && found.Running) {
      attachToEc(ecName);
      return;
    }
    await new Promise((r) => setTimeout(r, 1000));
  }
  setStatus("idle", "Idle");
  toast(`Timed out waiting for ${ecName} to start`, "error");
}

// ---------- attach (multi-session websockets) ----------
//
// Each attached EC has its own xterm.js Terminal in its own DOM div,
// its own WebSocket, its own scrollback. Only one is visible at a
// time (state.activeKey); the others sit hidden with display:none
// while their WSes keep feeding output. Switching between them is
// instant — no reconnect, no shell loss.
//
// openOrSwitchSession is the only public entry point. attachToEc
// remains as a thin shim from the EC-chip click path.

function attachToEc(ecName) {
  openOrSwitchSession(state.selectedNs, state.selectedPod, ecName);
}

function openOrSwitchSession(ns, pod, ec) {
  if (!ns || !pod || !ec) return;
  const key = sessionKey(ns, pod, ec);
  const existing = state.attached.get(key);
  if (existing && !existing.dead) {
    switchToSession(key);
    return;
  }
  // Re-opening a dead corpse → dispose first, then attach fresh.
  if (existing) closeSession(key);
  attachNewSession(ns, pod, ec);
}

function attachNewSession(ns, pod, ec) {
  const key = sessionKey(ns, pod, ec);

  const container = document.createElement("div");
  container.className = "term-pane";
  container.dataset.key = key;
  container.style.display = "none"; // unhidden by switchToSession
  $("terminal").appendChild(container);

  const term = new Terminal(TERM_OPTIONS);
  const fit = new FitAddon();
  term.loadAddon(fit);
  term.loadAddon(new WebLinksAddon());
  term.open(container);

  const rec = { ns, pod, ec, key, ws: null, term, fit, container, dead: false };
  state.attached.set(key, rec);

  // Per-session input + resize wiring. Each session writes only to
  // its own WebSocket.
  const encoder = new TextEncoder();
  term.onData((data) => {
    if (rec.ws && rec.ws.readyState === WebSocket.OPEN) {
      rec.ws.send(encoder.encode(data));
    }
  });
  term.onResize(({ cols, rows }) => {
    if (rec.ws && rec.ws.readyState === WebSocket.OPEN) {
      rec.ws.send(JSON.stringify({ type: "resize", cols, rows }));
    }
  });

  const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url =
    `${wsScheme}//${window.location.host}${BASE_PATH}` +
    `/term/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}/${encodeURIComponent(ec)}`;
  const ws = new WebSocket(url);
  ws.binaryType = "arraybuffer";
  rec.ws = ws;

  setStatus("connecting", "Connecting…");

  ws.addEventListener("open", () => {
    if (state.activeKey === key) {
      setStatus("connected", `${ns} :: ${pod} :: ${ec}`);
    }
    // Sync the remote PTY size to the local xterm, then nudge for a prompt.
    if (ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify({ type: "resize", cols: term.cols, rows: term.rows }));
      ws.send(encoder.encode("\r"));
    }
    if (state.activeKey === key) term.focus();
  });

  ws.addEventListener("message", (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      term.write(new Uint8Array(ev.data));
    } else {
      term.write(ev.data);
    }
  });

  ws.addEventListener("close", (ev) => {
    rec.dead = true;
    const reason = ev.reason || (ev.wasClean ? "closed" : "lost");
    term.writeln(`\r\n\x1b[2;37m[connection ${escapeAnsi(reason)}]\x1b[0m`);
    if (state.activeKey === key) {
      setStatus("idle", "Disconnected");
    }
    renderECBar();
    renderSessions();
  });

  ws.addEventListener("error", () => {
    if (state.activeKey === key) {
      setStatus("error", "Connection error");
    }
    toast(`WebSocket error: ${pod} :: ${ec}`, "error");
  });

  switchToSession(key);
}

// switchToSession makes `key`'s term-pane visible and pulls the
// sidebar selection along so the EC chip list reflects what you're
// looking at.
//
// Idempotent re-entry on purpose: clicking the already-active session
// in the dropdown after wandering the sidebar elsewhere should
// re-align the sidebar back to where the terminal lives. The inner
// selectNamespace/selectPod calls short-circuit when sidebar is
// already on the right ns/pod, so the cost when nothing's drifted is
// just a couple of equality checks.
function switchToSession(key) {
  for (const [k, rec] of state.attached) {
    rec.container.style.display = k === key ? "" : "none";
  }
  state.activeKey = key;
  const rec = state.attached.get(key);
  if (!rec) {
    state.selectedEc = null;
    $("terminal").parentElement.classList.remove("has-session");
    setStatus("idle", "Idle");
    renderECBar();
    updateTargetText();
    renderSessions();
    return;
  }
  $("terminal").parentElement.classList.add("has-session");
  // Drag the sidebar to follow the active session. selectNamespace
  // and selectPod no-op when already on that ns/pod, so this is safe.
  if (state.selectedNs !== rec.ns) selectNamespace(rec.ns);
  if (state.selectedPod !== rec.pod) selectPod(rec.pod);
  state.selectedEc = rec.ec;
  if (rec.dead) {
    setStatus("idle", "Disconnected");
  } else if (rec.ws && rec.ws.readyState === WebSocket.OPEN) {
    setStatus("connected", `${rec.ns} :: ${rec.pod} :: ${rec.ec}`);
  } else {
    setStatus("connecting", "Connecting…");
  }
  // Visible pane has dimensions now; refit + focus on the next tick.
  setTimeout(() => {
    try { rec.fit.fit(); } catch (_) {}
    rec.term.focus();
  }, 0);
  renderECBar();
  updateTargetText();
  renderSessions();
}

// closeSession terminates the WS, disposes the xterm, removes the
// pane from the DOM. If the closed session was the visible one,
// switches to another attached session if any, otherwise clears
// the terminal pane.
function closeSession(key) {
  const rec = state.attached.get(key);
  if (!rec) return;
  try { rec.ws && rec.ws.close(); } catch (_) {}
  try { rec.term.dispose(); } catch (_) {}
  rec.container.remove();
  state.attached.delete(key);
  if (state.activeKey === key) {
    state.activeKey = null;
    const nextKey = state.attached.keys().next().value || null;
    if (nextKey) {
      switchToSession(nextKey);
    } else {
      state.selectedEc = null;
      $("terminal").parentElement.classList.remove("has-session");
      setStatus("idle", "Idle");
      renderECBar();
      updateTargetText();
      renderSessions();
    }
  } else {
    renderSessions();
  }
}

// closeSessionByEc finds and closes any attached session whose EC
// name matches, regardless of which ns/pod (the EC name is unique
// per pod, so we also key by pod to be safe).
function closeSessionByEc(ns, pod, ec) {
  closeSession(sessionKey(ns, pod, ec));
}

// ---------- helpers ----------
function escapeHtml(s) {
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}
function escapeAnsi(s) {
  return String(s).replace(/[\x00-\x1f\x7f]/g, "");
}

// ---------- filters ----------
function setupFilters() {
  $("ns-filter").addEventListener("input", renderNamespaces);
  $("pod-filter").addEventListener("input", renderPods);
}

// ---------- workflow instructions ----------
//
// Three little steps at the top of the sidebar. Classes flow with
// the current selection so the strip doubles as a progress indicator:
//   step-1 → done as soon as a namespace is selected
//   step-2 → done as soon as a pod is selected; current after step-1
//   step-3 → current once both are picked (the next click is +Debugger)
function updateInstructions() {
  const s1 = $("step-1");
  const s2 = $("step-2");
  const s3 = $("step-3");
  if (!s1 || !s2 || !s3) return;
  const ns = !!state.selectedNs;
  const pod = !!state.selectedPod;
  s1.classList.toggle("done", ns);
  s1.classList.toggle("current", !ns);
  s2.classList.toggle("done", pod);
  s2.classList.toggle("current", ns && !pod);
  s3.classList.toggle("current", ns && pod);
}

// ---------- scroll affordance ----------
//
// The lists hide their overflow behind a thin (auto-hiding on macOS)
// scrollbar, so it wasn't obvious there were more items below the
// fold. We tag the parent panel with .has-overflow / .at-bottom so
// CSS can show a fade + ▾ at the bottom — and hide it once the user
// has actually scrolled to the end.
function updateOverflowHints() {
  for (const list of document.querySelectorAll(".panel .list")) {
    const panel = list.closest(".panel");
    if (!panel) continue;
    const overflow = list.scrollHeight - list.clientHeight > 1;
    const atBottom = list.scrollHeight - list.scrollTop - list.clientHeight < 2;
    panel.classList.toggle("has-overflow", overflow);
    panel.classList.toggle("at-bottom", !overflow || atBottom);
  }
}

function setupOverflowHints() {
  for (const panel of document.querySelectorAll(".panel")) {
    if (!panel.querySelector(".overflow-hint")) {
      const hint = document.createElement("div");
      hint.className = "overflow-hint";
      hint.innerHTML = "<span>▾</span>";
      panel.appendChild(hint);
    }
    const list = panel.querySelector(".list");
    if (list) {
      list.addEventListener("scroll", updateOverflowHints, { passive: true });
      // React to items being added/removed.
      new MutationObserver(updateOverflowHints).observe(list, {
        childList: true,
        subtree: true,
        characterData: true,
      });
    }
  }
  // Panel-size changes (sidebar resize, font load, etc.) flip overflow on/off.
  if (typeof ResizeObserver !== "undefined") {
    const ro = new ResizeObserver(updateOverflowHints);
    for (const panel of document.querySelectorAll(".panel")) ro.observe(panel);
  }
  updateOverflowHints();
}

// ---------- active sessions ----------
// Cluster-wide list of running porthole-* ECs, surfaced in a topbar
// dropdown so the user can jump back to a session they injected in
// a different namespace+pod without having to remember where they
// parked it. Backed by GET /debug/sessions; per-entry visibility is
// gated by OPA `list_pods` at request time.
let sessionsTimer = null;

async function loadSessions() {
  try {
    const r = await http("/debug/sessions");
    state.sessions = r.sessions || [];
    state.sessionsTotal = r.total || 0;
  } catch (e) {
    // 403/network failure: just collapse the widget. The rest of
    // the UI still works — this is a quality-of-life affordance.
    state.sessions = [];
    state.sessionsTotal = 0;
    console.warn("loadSessions:", e.message);
  }
  renderSessions();
}

function sessionAge(iso) {
  if (!iso) return "";
  const t = Date.parse(iso);
  if (!t) return "";
  const secs = Math.max(1, Math.floor((Date.now() - t) / 1000));
  if (secs < 60) return `${secs}s`;
  const mins = Math.floor(secs / 60);
  if (mins < 60) return `${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 24) return `${hours}h`;
  return `${Math.floor(hours / 24)}d`;
}

function renderSessions() {
  const wrap = $("sessions");
  const countEl = $("sessions-count");
  const labelEl = $("sessions-label");
  const menuEl = $("sessions-menu");
  // Filter out anything we've asked the server to terminate but
  // kubelet hasn't caught up on yet — same Set the EC bar uses.
  const list = (state.sessions || []).filter(
    (s) => !state.terminating.has(sessionKey(s.namespace, s.pod, s.ec)),
  );
  const total = state.sessionsTotal || 0;
  // Show the widget as long as there's any porthole activity —
  // either visible to this user, or cluster-wide (so "/ N total"
  // still tells the user something even if all the ECs live in
  // namespaces they can't browse).
  if (list.length === 0 && total === 0) {
    wrap.hidden = true;
    closeSessionsMenu();
    return;
  }
  wrap.hidden = false;
  countEl.textContent = String(list.length);
  labelEl.textContent = list.length === 1 ? "session" : "sessions";

  // Discrete total-cluster-EC badge. Always shown when the widget
  // is visible; tooltip explains it ignores access rights.
  const totalEl = $("sessions-total");
  totalEl.textContent = `/ ${total}`;
  totalEl.hidden = false;

  menuEl.innerHTML = "";
  for (const s of list) {
    const item = document.createElement("button");
    item.type = "button";
    item.className = "sessions-item";
    item.setAttribute("role", "menuitem");

    // Tri-state badge per entry:
    //   ● is-active   → attached in this tab AND currently visible
    //   ○ is-attached → attached in this tab, hidden behind another
    //   (none)        → not yet attached in this tab; first click
    //                   will open a fresh shell (empty)
    const key = sessionKey(s.namespace, s.pod, s.ec);
    let dotClass = "session-dot";
    let dotChar = "";
    if (state.activeKey === key) {
      item.classList.add("is-active");
      dotClass += " is-active";
      dotChar = "●";
    } else if (state.attached.has(key)) {
      item.classList.add("is-attached");
      dotClass += " is-attached";
      dotChar = "○";
    }

    // EC name (porthole-<hash>) goes in its own line so the user can
    // cross-reference with kubectl/logs without parsing a long meta
    // string. Image + age stay on a third muted line below.
    const meta = [s.image, sessionAge(s.started_at)]
      .filter(Boolean)
      .join(" · ");
    item.innerHTML =
      `<span class="${dotClass}" aria-hidden="true">${dotChar}</span>` +
      `<span class="sessions-body">` +
        `<span class="sessions-loc">` +
          `${escapeHtml(s.namespace)} / ` +
          `<span class="sessions-pod">${escapeHtml(s.pod)}</span>` +
        `</span>` +
        `<span class="sessions-ec">${escapeHtml(s.ec)}</span>` +
        `<span class="sessions-meta">${escapeHtml(meta)}</span>` +
      `</span>`;
    item.title = s.ec;
    item.addEventListener("click", () => jumpToSession(s));
    menuEl.appendChild(item);
  }
}

function jumpToSession(s) {
  closeSessionsMenu();
  // openOrSwitchSession opens a new WS+xterm pair the first time we
  // touch a session, otherwise just flips visibility — so jumping
  // back and forth between two sessions never re-spawns shells.
  // It also drags the sidebar to follow the active session.
  openOrSwitchSession(s.namespace, s.pod, s.ec);
}

function toggleSessionsMenu() {
  const menu = $("sessions-menu");
  const toggle = $("sessions-toggle");
  const willOpen = menu.hidden;
  menu.hidden = !willOpen;
  toggle.setAttribute("aria-expanded", String(willOpen));
}

function closeSessionsMenu() {
  const menu = $("sessions-menu");
  const toggle = $("sessions-toggle");
  if (menu.hidden) return;
  menu.hidden = true;
  toggle.setAttribute("aria-expanded", "false");
}

function setupSessionsWidget() {
  $("sessions-toggle").addEventListener("click", (e) => {
    e.stopPropagation();
    toggleSessionsMenu();
  });
  $("sessions-refresh").addEventListener("click", async (e) => {
    e.stopPropagation();
    const btn = e.currentTarget;
    btn.classList.add("busy");
    try { await loadSessions(); } finally {
      btn.classList.remove("busy");
    }
  });
  document.addEventListener("click", (e) => {
    if (!$("sessions").contains(e.target)) closeSessionsMenu();
  });
  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeSessionsMenu();
  });
  if (sessionsTimer) clearInterval(sessionsTimer);
  // 15s is short enough for the count to feel live, long enough that
  // a tab idle in the background isn't a constant API drumbeat. The
  // inject/cleanup paths also call loadSessions() directly so
  // user-driven changes show up immediately.
  sessionsTimer = setInterval(loadSessions, 15_000);
}

// ---------- boot ----------
async function init() {
  initTerminal();
  setStatus("idle", "Idle");
  setupFilters();
  setupOverflowHints();
  setupSessionsWidget();
  updateInstructions();
  $("inject-btn").addEventListener("click", injectDebugger);
  await loadConfig();
  await loadMe();
  await loadNamespaces();
  await loadSessions();
}

init().catch((e) => {
  console.error(e);
  toast("Init failed: " + e.message, "error");
});
