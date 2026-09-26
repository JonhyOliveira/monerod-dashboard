"use strict";

// The page arrives server-rendered (see templates/index.html). This script
// only keeps it fresh. Formatters must match internal/server/view.go so the
// first client render doesn't change any text. Numbers are pinned to en-US
// for the same reason.

const $ = (id) => document.getElementById(id);
const nf = new Intl.NumberFormat("en-US");

function fmtBytes(n) {
  if (!n) return "—";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0;
  while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return `${n.toFixed(i >= 3 ? 1 : 0)} ${units[i]}`;
}

function fmtHashrate(h) {
  if (!h) return "—";
  const units = ["H/s", "kH/s", "MH/s", "GH/s", "TH/s"];
  let i = 0;
  while (h >= 1000 && i < units.length - 1) { h /= 1000; i++; }
  return `${h.toFixed(2)} ${units[i]}`;
}

function fmtDuration(s) {
  if (s == null) return "—";
  const d = Math.floor(s / 86400), h = Math.floor(s % 86400 / 3600),
        m = Math.floor(s % 3600 / 60), sec = Math.floor(s % 60);
  if (d) return `${d}d ${h}h`;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${sec}s`;
  return `${sec}s`;
}

function fmtBigNumber(str) {
  if (!str) return "—";
  try { return BigInt(str).toLocaleString("en-US"); } catch { return str; }
}

function setText(id, v) { $(id).textContent = v; }

function renderWarnings(ws) {
  const ul = $("warnings");
  ul.replaceChildren(...ws.map((w) => {
    const li = document.createElement("li");
    li.className = w.level;
    li.textContent = w.message;
    return li;
  }));
  ul.hidden = ws.length === 0;
}

function renderUnreachable() {
  // Keep the last known values visible, but dimmed so they don't read as current.
  document.querySelector(".grid").classList.add("stale");
  $("state-dot").className = "dot unreachable";
  setText("state-label", "Unreachable");
}

function render(s) {
  renderWarnings(s.warnings || []);
  const updated = $("updated");
  updated.dateTime = s.fetched_at;
  updated.textContent = "updated " + new Date(s.fetched_at).toLocaleTimeString();
  if (!s.ok) { renderUnreachable(); return; }

  document.querySelector(".grid").classList.remove("stale");
  const n = s.node;
  const net = $("nettype");
  net.textContent = n.nettype || "unknown";
  net.hidden = false;
  $("restricted").hidden = !n.restricted;
  setText("version", n.version ? `v${n.version}` : "");

  $("state-dot").className = "dot " + n.state;
  setText("state-label", n.state_label);
  const pct = Math.min(100, n.sync_percent || 0);
  const syncBar = $("sync-bar");
  syncBar.style.width = pct + "%";
  syncBar.className = "fill " + (n.state === "synchronized" ? "ok" : "warn");
  setText("height", nf.format(n.height));
  setText("target-height", nf.format(n.target_height));
  // Floor so a node that is still behind never reads "100.00%".
  setText("sync-percent", (Math.floor(pct * 100) / 100).toFixed(2) + "%");

  setText("peers-out", nf.format(n.outgoing_peers));
  setText("peers-in", nf.format(n.incoming_peers));

  setText("hashrate", fmtHashrate(n.hashrate));
  setText("difficulty", fmtBigNumber(n.difficulty));
  setText("last-block", n.last_block_time ? fmtDuration(n.last_block_age_seconds) + " ago" : "—");
  setText("top-hash", n.top_block_hash || "");

  setText("txpool", nf.format(n.tx_pool_size));

  setText("db-size", fmtBytes(n.database_size));
  setText("free-space", fmtBytes(n.free_space));
  const diskBar = $("disk-bar");
  if (n.free_space && n.database_size) {
    // Share of (db + free) that is still free; a rough proxy for headroom.
    const freePct = n.free_space / (n.free_space + n.database_size) * 100;
    diskBar.style.width = (100 - freePct) + "%";
    const low = (s.warnings || []).some((w) => w.code === "low_disk");
    diskBar.className = "fill " + (low ? "err" : "ok");
  } else {
    diskBar.style.width = "0";
  }

  setText("uptime", n.uptime_seconds ? fmtDuration(n.uptime_seconds) : "—");
  setText("started", n.start_time ? new Date(n.start_time * 1000).toLocaleString() : "—");
}

// Refresh loop and indicator. The ring's fill is a CSS animation lasting
// --refresh-duration; restarting it each cycle keeps it in step with the timer.
const indicator = $("refresh");
let refreshSeconds = 5;
let timer, ticker, nextAt = 0, inflight = false, failed = false;

function setIndicator(state) {
  indicator.classList.remove("counting", "fetching");
  if (state === "counting") void indicator.offsetWidth; // restart the animation
  indicator.classList.add(state);
  indicator.classList.toggle("error", failed);
}

function tick() {
  const left = Math.max(0, Math.ceil((nextAt - Date.now()) / 1000));
  const label = inflight ? "Refreshing…" : `Next refresh in ${left}s`;
  indicator.title = label;
  indicator.setAttribute("aria-label", label + (inflight ? "" : " (click to refresh now)"));
  indicator.querySelector(".refresh-text").textContent = inflight ? "…" : `${left}s`;
}

function schedule() {
  clearTimeout(timer);
  nextAt = Date.now() + refreshSeconds * 1000;
  timer = setTimeout(poll, refreshSeconds * 1000);
  indicator.style.setProperty("--refresh-duration", refreshSeconds + "s");
  setIndicator("counting");
  tick();
}

function flashUpdated() {
  const el = $("updated");
  el.classList.remove("flash");
  void el.offsetWidth;
  el.classList.add("flash");
}

async function poll() {
  if (inflight) return;
  inflight = true;
  clearTimeout(timer);
  setIndicator("fetching");
  tick();
  try {
    const res = await fetch("api/status", { cache: "no-store" });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const s = await res.json();
    refreshSeconds = s.refresh_seconds || refreshSeconds;
    render(s);
    failed = false;
    flashUpdated();
  } catch (err) {
    failed = true;
    renderUnreachable();
    renderWarnings([{ level: "error", message: "Dashboard server unreachable: " + err.message }]);
  } finally {
    inflight = false;
    schedule();
  }
}

indicator.addEventListener("click", poll);

// Take over from the server render: re-render from the embedded status (this
// only localizes timestamps) and wait a full interval before the first poll.
try {
  const initial = JSON.parse($("initial").textContent);
  refreshSeconds = initial.refresh_seconds || refreshSeconds;
  render(initial);
} catch (err) {
  console.error("reading initial status:", err);
}
schedule();
ticker = setInterval(tick, 1000);
