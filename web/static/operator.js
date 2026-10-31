"use strict";
// Operator dashboard (plan 5.2 / Phase 8).

const REFRESH_MS = 5000;
const ROUTE_COLORS = ["#3b6fd8", "#d8613b", "#2e9e6a", "#8b5cf6", "#c0392b"];

const $ = (id) => document.getElementById(id);
let map, routeLayer, busLayer;
let routes = [];
let alertFilter = "ACTIVE,ACKNOWLEDGED";
let refreshTimer = null;
const openNotes = new Map(); // alert id -> note draft (kept across refreshes)

function showLogin(message) {
  clearInterval(refreshTimer);
  $("dash-view").hidden = true;
  $("logout").hidden = true;
  $("who").textContent = "";
  $("login-view").hidden = false;
  $("login-error").textContent = message || "";
  $("login-email").focus();
}

async function showDashboard(user) {
  $("login-view").hidden = true;
  $("dash-view").hidden = false;
  $("logout").hidden = false;
  $("who").textContent = `Signed in as ${user.email} (${user.role})`;
  if (!map) await initMap();
  await refresh();
  clearInterval(refreshTimer);
  refreshTimer = setInterval(refresh, REFRESH_MS);
}

async function initMap() {
  map = baseMap("op-map");
  routeLayer = L.layerGroup().addTo(map);
  busLayer = L.layerGroup().addTo(map);
  routes = await api("/api/v1/routes");
  const bounds = [];
  await Promise.all(routes.map(async (r, i) => {
    const [detail, stops] = await Promise.all([
      api(`/api/v1/routes/${encodeURIComponent(r.id)}`),
      api(`/api/v1/routes/${encodeURIComponent(r.id)}/stops`),
    ]);
    const color = ROUTE_COLORS[i % ROUTE_COLORS.length];
    r.color = color;
    if (detail.geometry?.length) {
      L.polyline(detail.geometry, { color, weight: 5, opacity: 0.65 }).bindTooltip(`Route ${r.route_number}`).addTo(routeLayer);
      bounds.push(...detail.geometry);
    }
    for (const s of stops) {
      L.circleMarker([s.latitude, s.longitude], { radius: 4, color: "#1d2433", fillColor: "#fff", fillOpacity: 1, weight: 1.5 })
        .bindTooltip(s.name).addTo(routeLayer);
    }
  }));
  if (bounds.length) map.fitBounds(bounds, { padding: [20, 20] });
}

async function refresh() {
  try {
    const [dash, table, alerts] = await Promise.all([
      api("/api/v1/operator/dashboard"),
      api("/api/v1/operator/routes"),
      api(`/api/v1/operator/alerts${alertFilter ? `?status=${alertFilter}` : ""}`),
    ]);
    renderTiles(dash);
    renderRouteTable(table, dash);
    renderAlerts(alerts);
    refreshBuses();
  } catch (err) {
    if (err.status === 401) showLogin("Your session has expired. Please log in again.");
    else console.error(err);
  }
}

function renderTiles(d) {
  $("t-routes").textContent = d.active_routes;
  $("t-vehicles").textContent = d.vehicles_with_recent_gps;
  $("t-high").textContent = d.high_routes.length;
  $("t-high").className = `tile-value${d.high_routes.length ? " danger" : ""}`;
  $("t-high-list").textContent = d.high_routes.length ? d.high_routes.join(", ") : "";
  $("t-alerts").textContent = d.active_alerts;
  $("t-alerts").className = `tile-value${d.active_alerts ? " danger" : ""}`;
  $("t-ack").textContent = d.acknowledged_alerts ? `+ ${d.acknowledged_alerts} acknowledged` : "";
}

function renderRouteTable(rows, d) {
  $("route-table").querySelector("tbody").replaceChildren(...rows.map((r) => el("tr", {},
    el("td", {}, el("strong", {}, r.route_number), el("br"), el("small", { class: "muted" }, r.route_name)),
    el("td", {},
      el("span", { class: `crowd-badge small ${r.crowd_status}` }, r.crowd_status),
      r.high_since ? el("div", { class: "small muted" }, `HIGH since ${clockTime(r.high_since)}`) : null,
      r.open_alert_status ? el("div", { class: "small danger-text" }, `Alert ${r.open_alert_status.toLowerCase()}`) : null),
    el("td", {}, `${r.valid_report_count}`, el("br"), el("small", { class: "muted" }, `${r.low_count} / ${r.medium_count} / ${r.high_count}`)),
    el("td", {}, ago(r.last_report_at)),
    el("td", {}, ago(r.last_gps_update)),
    el("td", {}, ago(r.last_analytics_update)),
    el("td", {}, `${r.vehicles_with_recent_gps} / ${r.active_trips}`))));
  $("route-note").textContent =
    `Status uses the last ${d.analytics_window_minutes} min of passenger reports (one per passenger). ` +
    `UNKNOWN means too few reports, which is not the same as low crowding. ` +
    `Buses count as having recent GPS if updated in the last ${d.stale_after_seconds}s.`;
}

function renderAlerts(alerts) {
  const box = $("alerts");
  if (!alerts.length) {
    box.replaceChildren(el("p", { class: "muted" }, alertFilter === "RESOLVED" ? "No resolved alerts yet." : "No open alerts."));
    return;
  }
  // Keep focus/typing in a note box stable across refreshes.
  const focused = document.activeElement?.dataset?.noteFor;
  box.replaceChildren(...alerts.map(alertCard));
  if (focused) {
    const input = box.querySelector(`[data-note-for="${CSS.escape(focused)}"]`);
    if (input) { input.focus(); input.setSelectionRange(input.value.length, input.value.length); }
  }
}

function alertCard(a) {
  const open = a.status !== "RESOLVED";
  const note = el("input", {
    type: "text", maxlength: "500", placeholder: "Resolution note (optional)", "data-note-for": a.id,
    value: openNotes.get(a.id) || "",
    oninput: (e) => openNotes.set(a.id, e.target.value),
  });
  const feedback = el("p", { class: "feedback small" });

  async function act(action) {
    feedback.className = "feedback small";
    feedback.textContent = "Saving…";
    try {
      await api(`/api/v1/operator/alerts/${encodeURIComponent(a.id)}`, {
        method: "PATCH", body: { action, note: action === "resolve" ? note.value : "" },
      });
      openNotes.delete(a.id);
      await refresh();
    } catch (err) {
      if (err.status === 401) return showLogin("Your session has expired. Please log in again.");
      feedback.className = "feedback error small";
      feedback.textContent = err.message;
    }
  }

  return el("article", { class: `alert-card ${a.status}` },
    el("div", { class: "alert-head" },
      el("strong", {}, `Route ${a.route_number || a.target_id}`),
      el("span", { class: "muted small" }, a.route_name),
      badge(a.status, a.status)),
    el("p", {}, a.reason),
    el("p", { class: "recommendation" }, "Recommendation: ", el("strong", {}, a.recommendation)),
    el("dl", { class: "alert-meta" },
      el("dt", {}, "HIGH since"), el("dd", {}, clockTime(a.high_since)),
      el("dt", {}, "Created"), el("dd", {}, `${clockTime(a.created_at)} (${ago(a.created_at)})`),
      a.acknowledged_at ? [el("dt", {}, "Acknowledged"), el("dd", {}, `${clockTime(a.acknowledged_at)} by ${a.acknowledged_by ?? "?"}`)] : null,
      a.resolved_at ? [el("dt", {}, "Resolved"), el("dd", {}, `${clockTime(a.resolved_at)} by ${a.resolved_by ?? "?"}`)] : null,
      a.resolution_note ? [el("dt", {}, "Note"), el("dd", {}, a.resolution_note)] : null),
    open && a.recovered_at
      ? el("p", { class: "recovered" }, `Crowding reliably dropped below HIGH at ${clockTime(a.recovered_at)}. Ready for review.`)
      : null,
    open ? el("div", { class: "alert-actions" },
      a.status === "ACTIVE" ? el("button", { type: "button", class: "secondary", onclick: () => act("acknowledge") }, "Acknowledge") : null,
      note,
      el("button", { type: "button", onclick: () => act("resolve") }, "Resolve")) : null,
    feedback);
}

async function refreshBuses() {
  const all = await Promise.all(routes.map((r) =>
    api(`/api/v1/routes/${encodeURIComponent(r.id)}/vehicles`).then((d) => d.vehicles.map((v) => ({ ...v, route: r }))).catch(() => [])));
  busLayer.clearLayers();
  for (const v of all.flat()) {
    if (v.latitude == null) continue;
    L.circleMarker([v.latitude, v.longitude], busStyle(v.freshness))
      .bindTooltip(`${v.label} · ${v.route.route_number}`)
      .bindPopup(busPopup(v))
      .addTo(busLayer);
  }
}

$("login-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  $("login-error").textContent = "";
  try {
    const res = await api("/api/v1/auth/login", {
      method: "POST", body: { email: $("login-email").value, password: $("login-password").value },
    });
    $("login-password").value = "";
    await showDashboard(res.user);
  } catch (err) {
    $("login-error").textContent = err.status === 429 ? "Too many attempts. Please wait a minute." : err.message;
  }
});

$("logout").addEventListener("click", async () => {
  await api("/api/v1/auth/logout", { method: "POST" }).catch(() => {});
  showLogin("You have been logged out.");
});

document.querySelectorAll(".tab").forEach((t) => t.addEventListener("click", () => {
  document.querySelectorAll(".tab").forEach((x) => x.classList.toggle("active", x === t));
  alertFilter = t.dataset.filter;
  refresh();
}));

api("/api/v1/auth/me").then(showDashboard).catch(() => showLogin());
