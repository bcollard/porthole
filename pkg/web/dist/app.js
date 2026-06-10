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
  selectedNs: null,
  selectedPod: null,
  selectedEc: null,
  term: null,
  fit: null,
  ws: null,
};

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
function initTerminal() {
  state.term = new Terminal({
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
  });
  state.fit = new FitAddon();
  state.term.loadAddon(state.fit);
  state.term.loadAddon(new WebLinksAddon());
  state.term.open($("terminal"));
  try { state.fit.fit(); } catch (_) {}

  const encoder = new TextEncoder();
  state.term.onData((data) => {
    if (state.ws && state.ws.readyState === WebSocket.OPEN) {
      // Send stdin as a binary frame — text frames are reserved for
      // JSON control messages (resize, etc).
      state.ws.send(encoder.encode(data));
    }
  });

  state.term.onResize(({ cols, rows }) => {
    sendResize(cols, rows);
  });

  const ro = new ResizeObserver(() => {
    try { state.fit.fit(); } catch (_) {}
  });
  ro.observe($("terminal"));
}

function sendResize(cols, rows) {
  if (!state.ws || state.ws.readyState !== WebSocket.OPEN) return;
  state.ws.send(JSON.stringify({ type: "resize", cols, rows }));
}

function clearTerminal() {
  state.term.reset();
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

async function loadMe() {
  const userEl = $("user");
  const nameEl = $("user-name");
  const logoutEl = $("user-logout");
  try {
    const me = await http("/api/me");
    state.me = me;
    nameEl.textContent = displayName(me);
    nameEl.title = me.email || me.sub || "";
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
  // let us remove them — so a terminated EC lingers forever. Hide
  // them: most users only want to see what's still alive.
  // `pendingTerminate` is a client-only flag we set while a kill is
  // in flight; the pill stays visible (with a spinner) until the
  // next server reload either flips Terminated=true or omits it.
  const visible = state.ecs.filter((ec) => !ec.Terminated);

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
  if (ec.Name === state.selectedEc) chip.classList.add("active");
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
  if (state.selectedEc === ecName) closeWebsocket();
  renderECBar();

  try {
    await http(
      `/debug/cleanup/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}/${encodeURIComponent(ecName)}`,
      { method: "POST" },
    );
    await loadEphemeralContainers(ns, pod);
  } catch (e) {
    ec.pendingTerminate = false;
    renderECBar();
    toast("Terminate failed: " + e.message, "error");
  }
}

async function cleanupPod(btn) {
  if (!state.selectedNs || !state.selectedPod) return;
  const ns = state.selectedNs;
  const pod = state.selectedPod;
  closeWebsocket();

  // Mark every porthole-* running EC pending, so each pill shows a
  // spinner alongside the button-level "Cleaning up…" indicator.
  for (const ec of state.ecs) {
    if (ec.Running && ec.Name.startsWith("porthole-")) {
      ec.pendingTerminate = true;
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
  }
}

function updateTargetText() {
  const el = $("target-text");
  if (!state.selectedPod) {
    el.textContent = "no pod selected";
    return;
  }
  let s = `${state.selectedNs}/${state.selectedPod}`;
  if (state.selectedEc) s += ` :: ${state.selectedEc}`;
  el.textContent = s;
}

// ---------- selection handlers ----------
function selectNamespace(ns) {
  if (state.selectedNs === ns) return;
  state.selectedNs = ns;
  state.selectedPod = null;
  state.selectedEc = null;
  state.pods = [];
  state.ecs = [];
  state.services = [];
  state.podDetail = null;
  $("ns-current").textContent = ns;
  $("ns-current-svc").textContent = ns;
  $("inject-btn").disabled = true;
  closeWebsocket();
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
  state.selectedEc = null;
  state.ecs = [];
  state.podDetail = null;
  $("inject-btn").disabled = false;
  closeWebsocket();
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

// ---------- attach (websocket) ----------
function closeWebsocket() {
  if (state.ws) {
    try { state.ws.close(); } catch (_) {}
    state.ws = null;
  }
  state.selectedEc = null;
  $("terminal").parentElement.classList.remove("has-session");
  setStatus("idle", "Idle");
  updateTargetText();
}

function attachToEc(ecName) {
  closeWebsocket();
  state.selectedEc = ecName;
  renderECBar();
  updateTargetText();

  const wsScheme = window.location.protocol === "https:" ? "wss:" : "ws:";
  const url =
    `${wsScheme}//${window.location.host}${BASE_PATH}` +
    `/term/${encodeURIComponent(state.selectedNs)}/${encodeURIComponent(state.selectedPod)}/${encodeURIComponent(ecName)}`;

  setStatus("connecting", "Connecting…");
  clearTerminal();
  $("terminal").parentElement.classList.add("has-session");

  const ws = new WebSocket(url);
  ws.binaryType = "arraybuffer";
  state.ws = ws;

  ws.addEventListener("open", () => {
    setStatus("connected", `${state.selectedPod} :: ${ecName}`);
    state.term.focus();
    // Sync the remote PTY size to the local xterm, then nudge for a prompt.
    sendResize(state.term.cols, state.term.rows);
    const encoder = new TextEncoder();
    ws.send(encoder.encode("\r"));
  });

  ws.addEventListener("message", (ev) => {
    if (ev.data instanceof ArrayBuffer) {
      state.term.write(new Uint8Array(ev.data));
    } else {
      state.term.write(ev.data);
    }
  });

  ws.addEventListener("close", (ev) => {
    if (state.ws === ws) {
      state.ws = null;
      const reason = ev.reason || (ev.wasClean ? "closed" : "lost");
      setStatus("idle", "Disconnected");
      state.term.writeln(`\r\n\x1b[2;37m[connection ${escapeAnsi(reason)}]\x1b[0m`);
    }
  });

  ws.addEventListener("error", () => {
    setStatus("error", "Connection error");
    toast("WebSocket error", "error");
  });
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

// ---------- boot ----------
async function init() {
  initTerminal();
  setStatus("idle", "Idle");
  setupFilters();
  setupOverflowHints();
  updateInstructions();
  $("inject-btn").addEventListener("click", injectDebugger);
  await loadConfig();
  await loadMe();
  await loadNamespaces();
}

init().catch((e) => {
  console.error(e);
  toast("Init failed: " + e.message, "error");
});
