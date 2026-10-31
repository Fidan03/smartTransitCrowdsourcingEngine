"use strict";
// Passenger interface (plan 5.1 / Phase 7).

const POLL_MS = 3000;
const CROWD_POLL_MS = 5000;

const CROWD_TEXT = {
  LOW: "Low: seats likely available",
  MEDIUM: "Medium: standing room",
  HIGH: "High: very crowded",
  UNKNOWN: "Not enough recent reports to say",
};

const map = baseMap("map");
const routeLayer = L.layerGroup().addTo(map);
const busLayer = L.layerGroup().addTo(map);
const busMarkers = new Map();
let stopMarkers = new Map();

const state = { routeId: null, stopId: null, stops: [], vehicles: [] };
let timers = [];

// Anonymous, random per-browser ID used only for duplicate-report control.
function clientId() {
  let id = localStorage.getItem("st_client_id");
  if (!id) {
    id = crypto.randomUUID ? crypto.randomUUID() : Array.from(crypto.getRandomValues(new Uint8Array(16)), (b) => b.toString(16).padStart(2, "0")).join("");
    localStorage.setItem("st_client_id", id);
  }
  return id;
}

const $ = (id) => document.getElementById(id);

async function init() {
  const routes = await api("/api/v1/routes");
  $("route-select").replaceChildren(...routes.map((r) => el("option", { value: r.id }, `${r.route_number}: ${r.name}`)));
  $("route-select").addEventListener("change", (e) => selectRoute(e.target.value));
  $("stop-select").addEventListener("change", (e) => selectStop(e.target.value));
  document.querySelectorAll("button.level").forEach((b) => b.addEventListener("click", () => submitReport(b.dataset.level)));
  if (routes.length) await selectRoute(routes[0].id);
  else $("status").textContent = "No routes available.";
}

async function selectRoute(routeId) {
  state.routeId = routeId;
  timers.forEach(clearInterval);
  timers = [];
  routeLayer.clearLayers();
  busLayer.clearLayers();
  busMarkers.clear();
  stopMarkers = new Map();

  const [route, stops] = await Promise.all([
    api(`/api/v1/routes/${encodeURIComponent(routeId)}`),
    api(`/api/v1/routes/${encodeURIComponent(routeId)}/stops`),
  ]);
  if (routeId !== state.routeId) return;
  state.stops = stops;

  if (route.geometry?.length) {
    const line = L.polyline(route.geometry, { color: "#3b6fd8", weight: 5, opacity: 0.7 }).addTo(routeLayer);
    map.fitBounds(line.getBounds(), { padding: [30, 30] });
  }
  for (const s of stops) {
    const m = L.circleMarker([s.latitude, s.longitude], { radius: 6, color: "#1d2433", fillColor: "#fff", fillOpacity: 1, weight: 2 })
      .bindTooltip(`${s.stop_order + 1}. ${s.name}`)
      .on("click", () => { $("stop-select").value = s.id; selectStop(s.id); })
      .addTo(routeLayer);
    stopMarkers.set(s.id, m);
  }
  $("stop-select").replaceChildren(...stops.map((s) => el("option", { value: s.id }, `${s.stop_order + 1}. ${s.name}`)));

  selectStop(stops[Math.min(2, stops.length - 1)]?.id);
  $("stop-select").value = state.stopId;

  await Promise.all([refreshVehicles(), refreshCrowd()]);
  timers.push(setInterval(refreshVehicles, POLL_MS), setInterval(refreshArrivals, POLL_MS), setInterval(refreshCrowd, CROWD_POLL_MS));
}

function selectStop(stopId) {
  state.stopId = stopId;
  const stop = state.stops.find((s) => s.id === stopId);
  $("arrivals-stop-name").textContent = stop ? stop.name : "this stop";
  $("report-stop-name").textContent = stop ? stop.name : "the selected stop";
  for (const [id, m] of stopMarkers) {
    const selected = id === stopId;
    m.setRadius(selected ? 9 : 6).setStyle({ fillColor: selected ? "#3b6fd8" : "#fff" });
  }
  $("arrivals").replaceChildren(el("li", { class: "muted" }, "Loading…"));
  refreshArrivals();
}

async function refreshVehicles() {
  const routeId = state.routeId;
  let data;
  try {
    data = await api(`/api/v1/routes/${encodeURIComponent(routeId)}/vehicles`);
  } catch (err) {
    $("status").textContent = `Could not load buses: ${err.message}`;
    return;
  }
  if (routeId !== state.routeId) return;
  state.vehicles = data.vehicles;

  const seen = new Set();
  for (const v of data.vehicles) {
    if (v.latitude == null) continue;
    seen.add(v.vehicle_id);
    let m = busMarkers.get(v.vehicle_id);
    if (!m) {
      m = L.circleMarker([v.latitude, v.longitude]).addTo(busLayer);
      busMarkers.set(v.vehicle_id, m);
    }
    m.setLatLng([v.latitude, v.longitude]).setStyle(busStyle(v.freshness)).bindPopup(busPopup(v)).bindTooltip(v.label);
  }
  for (const [id, m] of busMarkers) {
    if (!seen.has(id)) { busLayer.removeLayer(m); busMarkers.delete(id); }
  }
  renderVehicleTable(data.vehicles);
  renderVehicleOptions(data.vehicles);
}

function renderVehicleTable(vehicles) {
  const table = $("vehicle-table");
  if (!vehicles.length) {
    table.hidden = true;
    $("status").textContent = "No buses are currently reporting on this route.";
    return;
  }
  table.hidden = false;
  const notFresh = vehicles.filter((v) => v.freshness !== "FRESH").length;
  $("status").textContent = notFresh
    ? `${notFresh} of ${vehicles.length} bus(es) have out-of-date or missing GPS.`
    : `${vehicles.length} bus(es) reporting.`;
  table.querySelector("tbody").replaceChildren(...vehicles.map((v) => el("tr", {},
    el("td", {}, v.label),
    el("td", {}, DIRECTION_LABEL[v.direction] ?? "–"),
    el("td", {}, v.speed_kmh != null && v.freshness === "FRESH" ? `${v.speed_kmh} km/h` : "–"),
    el("td", {}, ageText(v.age_seconds)),
    el("td", {}, badge(v.freshness, v.freshness)))));
}

function renderVehicleOptions(vehicles) {
  const sel = $("report-vehicle");
  const current = sel.value;
  sel.replaceChildren(
    el("option", { value: "" }, "Not sure / not on a bus"),
    ...vehicles.map((v) => el("option", { value: v.vehicle_id }, v.label)));
  if (vehicles.some((v) => v.vehicle_id === current)) sel.value = current;
}

async function refreshArrivals() {
  const { routeId, stopId } = state;
  if (!stopId) return;
  let data;
  try {
    data = await api(`/api/v1/stops/${encodeURIComponent(stopId)}/arrivals?route_id=${encodeURIComponent(routeId)}`);
  } catch (err) {
    $("arrivals").replaceChildren(el("li", { class: "muted" }, `Could not load arrivals: ${err.message}`));
    return;
  }
  if (stopId !== state.stopId || routeId !== state.routeId) return;

  if (!data.arrivals.length) {
    $("arrivals").replaceChildren(el("li", { class: "muted" }, "No buses currently heading to this stop."));
    return;
  }
  $("arrivals").replaceChildren(...data.arrivals.map((a) => {
    const who = `${a.route_number} · ${a.vehicle_label}`;
    if (a.status !== "ESTIMATED") {
      const why = { gps_stale: "GPS out of date", no_gps: "No GPS data", off_route: "Bus is off route" }[a.reason] || "Unavailable";
      return el("li", { class: "arrival unavailable" },
        el("span", { class: "eta" }, "–"),
        el("span", {}, who, el("br"), el("small", { class: "muted" }, `${why}: no estimate`)));
    }
    const etaText = a.eta_seconds < 60 ? (a.reason === "at_stop" ? "Now" : "<1 min") : `${a.eta_minutes} min`;
    const dist = a.distance_m >= 1000 ? `${(a.distance_m / 1000).toFixed(1)} km` : `${Math.round(a.distance_m)} m`;
    return el("li", { class: "arrival" },
      el("span", { class: "eta" }, etaText),
      el("span", {}, who, el("br"),
        el("small", { class: "muted" },
          `${dist} · ${a.stops_away} stop${a.stops_away === 1 ? "" : "s"} away · `,
          a.confidence === "HIGH" ? "based on recent speed" : "rough estimate")));
  }));
}

async function refreshCrowd() {
  const routeId = state.routeId;
  let s;
  try {
    s = await api(`/api/v1/routes/${encodeURIComponent(routeId)}/crowd-status`);
  } catch (err) {
    $("crowd-detail").textContent = `Could not load crowding: ${err.message}`;
    return;
  }
  if (routeId !== state.routeId) return;
  const b = $("crowd-badge");
  b.className = `crowd-badge ${s.status}`;
  b.textContent = s.status;
  const evidence = s.valid_report_count
    ? `${s.valid_report_count} report${s.valid_report_count === 1 ? "" : "s"} in the last ${s.window_minutes} min · latest ${ago(s.last_report_at)}`
    : `No reports in the last ${s.window_minutes} min`;
  const detail = s.status === "UNKNOWN" && s.valid_report_count
    ? `${CROWD_TEXT.UNKNOWN} (need ${s.min_reports}).`
    : CROWD_TEXT[s.status] + ".";
  $("crowd-detail").replaceChildren(detail, el("br"), el("small", {}, evidence));
}

async function submitReport(level) {
  const fb = $("report-feedback");
  const buttons = document.querySelectorAll("button.level");
  buttons.forEach((b) => (b.disabled = true));
  fb.className = "feedback";
  fb.textContent = "Sending…";
  const body = { route_id: state.routeId, crowd_level: level };
  if ($("report-at-stop").checked && state.stopId) body.stop_id = state.stopId;
  if ($("report-vehicle").value) body.vehicle_id = $("report-vehicle").value;
  try {
    await api("/api/v1/crowd-reports", { method: "POST", body, headers: { "X-Client-Id": clientId() } });
    fb.className = "feedback ok";
    fb.textContent = `Thanks! Your ${level} report was received.`;
    refreshCrowd();
  } catch (err) {
    fb.className = "feedback error";
    if (err.code === "duplicate_report") {
      fb.textContent = `You reported recently. You can report again in about ${Math.ceil((err.retryAfter || 60) / 60)} min.`;
    } else if (err.code === "rate_limited") {
      fb.textContent = "Too many reports from this network. Please try again shortly.";
    } else {
      fb.textContent = `Could not send report: ${err.message}`;
    }
  } finally {
    buttons.forEach((b) => (b.disabled = false));
  }
}

init().catch((err) => { $("status").textContent = `Could not load routes: ${err.message}`; });
