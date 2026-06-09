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
    nameEl.textContent = displayName(me);
    nameEl.title = me.email || me.sub || "";
    // AUTH_DISABLED stamps a fixed local-dev principal; there's no
    // OIDC layer to log out from, so hide the link.
    if (me.sub === "local-dev") {
      logoutEl.hidden = true;
    } else {
      logoutEl.href = BASE_PATH + "/logout";
    }
    userEl.hidden = false;
  } catch (e) {
    // /api/me requires auth; if it fails we just don't render the
    // user widget — the rest of the UI degrades cleanly.
    console.warn("loadMe:", e.message);
  }
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
  if (state.ecs.length === 0) {
    bar.innerHTML = `<span class="ec-empty">No ephemeral containers. Inject one →</span>`;
    return;
  }
  bar.innerHTML = "";
  for (const ec of state.ecs) {
    const chip = document.createElement("button");
    chip.className = "ec-chip";
    if (ec.Running) chip.classList.add("running");
    if (ec.Name === state.selectedEc) chip.classList.add("active");
    chip.innerHTML = `<span class="ec-dot"></span><span>${escapeHtml(ec.Name)}</span>`;
    chip.title = ec.Running ? "Click to attach" : "Container is not running";
    if (ec.Running) {
      chip.addEventListener("click", () => attachToEc(ec.Name));
    } else {
      chip.disabled = true;
    }
    bar.appendChild(chip);
  }

  // "Clean up all" only when there's at least one porthole-* running EC.
  const cleanable = state.ecs.some(
    (e) => e.Running && e.Name.startsWith("porthole-"),
  );
  if (cleanable) {
    const btn = document.createElement("button");
    btn.className = "ec-cleanup";
    btn.title = "Terminate every porthole-injected ephemeral container in this pod";
    btn.textContent = "Clean up all";
    btn.addEventListener("click", () => cleanupPod());
    bar.appendChild(btn);
  }
}

async function cleanupPod() {
  if (!state.selectedNs || !state.selectedPod) return;
  const ns = state.selectedNs;
  const pod = state.selectedPod;
  closeWebsocket();
  try {
    const res = await http(
      `/debug/cleanup/${encodeURIComponent(ns)}/${encodeURIComponent(pod)}`,
      { method: "POST" },
    );
    const ok = (res.results || []).filter((r) => r.ok).length;
    toast(`Terminated ${ok} ephemeral container${ok === 1 ? "" : "s"}`, "success");
  } catch (e) {
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
  $("ns-current").textContent = ns;
  $("inject-btn").disabled = true;
  closeWebsocket();
  renderNamespaces();
  renderPods();
  renderECBar();
  updateTargetText();
  loadPods(ns);
}

function selectPod(pod) {
  if (state.selectedPod === pod) return;
  state.selectedPod = pod;
  state.selectedEc = null;
  state.ecs = [];
  $("inject-btn").disabled = false;
  closeWebsocket();
  renderPods();
  renderECBar();
  updateTargetText();
  loadEphemeralContainers(state.selectedNs, pod);
}

// ---------- inject ----------
async function injectDebugger() {
  if (!state.selectedNs || !state.selectedPod) return;
  const btn = $("inject-btn");
  btn.disabled = true;
  const originalLabel = btn.innerHTML;
  btn.innerHTML = "<span>Injecting…</span>";
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

// ---------- boot ----------
async function init() {
  initTerminal();
  setStatus("idle", "Idle");
  setupFilters();
  $("inject-btn").addEventListener("click", injectDebugger);
  await loadConfig();
  await loadMe();
  await loadNamespaces();
}

init().catch((e) => {
  console.error(e);
  toast("Init failed: " + e.message, "error");
});
