"use strict";
// Shared helpers for the passenger and operator pages.

const DIRECTION_LABEL = { 0: "Outbound", 1: "Return" };

class ApiError extends Error {
  constructor(status, code, message, retryAfter) {
    super(message);
    this.status = status;
    this.code = code;
    this.retryAfter = retryAfter;
  }
}

async function api(path, options = {}) {
  const opts = { credentials: "same-origin", ...options, headers: { Accept: "application/json", ...(options.headers || {}) } };
  if (opts.body !== undefined && typeof opts.body !== "string") {
    opts.body = JSON.stringify(opts.body);
    opts.headers["Content-Type"] = "application/json";
  }
  const res = await fetch(path, opts);
  const banner = document.getElementById("demo-banner");
  if (banner) banner.hidden = res.headers.get("X-Data-Mode") !== "SIMULATED";
  const body = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, body?.error?.code, body?.error?.message || `HTTP ${res.status}`,
      Number(res.headers.get("Retry-After")) || null);
  }
  return body.data;
}

function el(tag, attrs = {}, ...children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) {
    if (k === "class") node.className = v;
    else if (k.startsWith("on")) node.addEventListener(k.slice(2), v);
    else if (v !== null && v !== undefined && v !== false) node.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c !== null && c !== undefined && c !== false) node.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return node;
}

function ago(iso, nowMs = Date.now()) {
  if (!iso) return "never";
  const sec = Math.max(0, (nowMs - new Date(iso).getTime()) / 1000);
  if (sec < 60) return `${Math.round(sec)}s ago`;
  if (sec < 3600) return `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s ago`;
  return `${Math.floor(sec / 3600)}h ${Math.floor((sec % 3600) / 60)}m ago`;
}

function ageText(sec) {
  if (sec == null) return "never";
  if (sec < 60) return `${Math.round(sec)}s ago`;
  return `${Math.floor(sec / 60)}m ${Math.round(sec % 60)}s ago`;
}

function clockTime(iso) {
  return iso ? new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" }) : "–";
}

function badge(text, cls) {
  return el("span", { class: `badge ${cls}` }, text.replace("_", " "));
}

function baseMap(elementId) {
  const map = L.map(elementId).setView([40.395, 49.86], 12);
  L.tileLayer("https://{s}.tile.openstreetmap.org/{z}/{x}/{y}.png", {
    maxZoom: 19,
    attribution: "&copy; OpenStreetMap contributors",
  }).addTo(map);
  return map;
}

function busStyle(freshness) {
  return freshness === "FRESH"
    ? { radius: 9, color: "#0f5132", fillColor: "#20c06b", fillOpacity: 0.95, weight: 2, dashArray: null }
    : { radius: 9, color: "#6c757d", fillColor: "#adb5bd", fillOpacity: 0.6, weight: 2, dashArray: "3 3" };
}

function busPopup(v) {
  return el("div", {},
    el("strong", {}, v.label), el("br"),
    `Direction: ${DIRECTION_LABEL[v.direction] ?? "unknown"}`, el("br"),
    `Speed: ${v.speed_kmh != null && v.freshness === "FRESH" ? v.speed_kmh + " km/h" : "n/a"}`, el("br"),
    `Updated: ${ageText(v.age_seconds)}`, el("br"),
    v.freshness === "STALE" ? el("em", {}, "Position is out of date") : null,
    v.is_simulated ? el("div", { class: "small muted" }, "Simulated position") : null);
}
