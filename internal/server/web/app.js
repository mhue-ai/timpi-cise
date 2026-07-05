// Dashboard front-end. Polls /api/status, renders metrics + recent results, and
// drives the start/stop, configuration, CSV-upload, and download endpoints.
// No external dependencies.

const $ = (id) => document.getElementById(id);

function fmtDuration(secs) {
  secs = Math.max(0, Math.floor(secs));
  const h = Math.floor(secs / 3600);
  const m = Math.floor((secs % 3600) / 60);
  const s = secs % 60;
  if (h) return `${h}h ${m}m`;
  if (m) return `${m}m ${s}s`;
  return `${s}s`;
}

function fmtTime(iso) {
  if (!iso) return "";
  return new Date(iso).toLocaleTimeString();
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

async function refresh() {
  try {
    const d = await (await fetch("/api/status")).json();
    const m = d.metrics;

    $("mSent").textContent = m.sent;
    $("mOK").textContent = m.ok;
    $("mFail").textContent = m.failed;
    $("mZero").textContent = m.ok ? `${(m.zero_result_rate * 100).toFixed(1)}%` : "—";
    $("mPct").textContent = m.sent ? `${m.p50_ms} / ${m.p95_ms} / ${m.p99_ms} ms` : "—";
    if (m.assert_run) {
      const failEl = m.assert_fail > 0 ? `<span class="bad">${m.assert_fail} fail</span>` : `<span class="ok">all pass</span>`;
      $("mAssert").innerHTML = `${m.assert_run - m.assert_fail}/${m.assert_run} · ${failEl}`;
    } else {
      $("mAssert").textContent = "off";
    }
    $("mUp").textContent = fmtDuration(m.uptime_seconds);
    $("mMode").textContent = `${d.mode} · ${d.adapter}`;
    renderSparklines(m.series || []);

    renderAlerts(d.alerts || []);
    $("logDir").textContent = d.log_dir || "—";
    $("csvPath").textContent = d.results_csv_path || "(disabled)";

    // CSV source status line.
    const cs = $("csvStatus");
    if (d.source === "csv") {
      cs.innerHTML = d.csv_error
        ? `<span class="st-bad">${esc(d.csv_error)}</span>`
        : `Loaded <strong>${d.csv_count}</strong> queries from your list.`;
    }

    const pill = $("pill");
    if (m.running) {
      pill.textContent = "running";
      pill.className = "pill pill-running";
      $("startBtn").disabled = true;
      $("stopBtn").disabled = false;
    } else {
      pill.textContent = "stopped";
      pill.className = "pill pill-stopped";
      $("startBtn").disabled = false;
      $("stopBtn").disabled = true;
    }

    renderLive(m.recent || [], m.running);
    renderResults(m.recent || []);
  } catch (e) { /* server not up yet */ }
}

function hostOf(u) {
  try { return new URL(u).host; } catch { return ""; }
}

function renderAlerts(alerts) {
  const b = $("alertBanner");
  if (!alerts.length) { b.classList.add("hidden"); b.innerHTML = ""; return; }
  b.classList.remove("hidden");
  b.innerHTML = `<strong>⚠ ${alerts.length} alert${alerts.length > 1 ? "s" : ""} firing:</strong> ` +
    alerts.map(esc).join(" · ");
}

// renderSparklines draws two inline SVG polylines (avg latency and success rate)
// from the per-minute time series. No external chart library.
function renderSparklines(series) {
  const W = 300, H = 48, pad = 3;
  const drawn = (id, values, maxLabelEl, fmtMax, color) => {
    const svg = $(id);
    if (!values.length) { svg.innerHTML = ""; if (maxLabelEl) $(maxLabelEl).textContent = ""; return; }
    const max = Math.max(1, ...values);
    if (maxLabelEl) $(maxLabelEl).textContent = fmtMax(max);
    const n = values.length;
    const x = (i) => n === 1 ? W / 2 : pad + (i * (W - 2 * pad)) / (n - 1);
    const y = (v) => H - pad - (v / max) * (H - 2 * pad);
    const pts = values.map((v, i) => `${x(i).toFixed(1)},${y(v).toFixed(1)}`).join(" ");
    const area = `${pad},${H - pad} ${pts} ${x(n - 1).toFixed(1)},${H - pad}`;
    svg.innerHTML =
      `<polygon points="${area}" fill="${color}" fill-opacity="0.12" />` +
      `<polyline points="${pts}" fill="none" stroke="${color}" stroke-width="1.5" />`;
  };
  drawn("sparkLat", series.map((p) => p.avg_latency_ms), "sparkLatMax", (m) => `${m} ms`, "#4f8cff");
  drawn("sparkOk", series.map((p) => p.sent ? Math.round((p.ok / p.sent) * 100) : 0), "sparkOkMax", () => "100%", "#3fb950");
}

// renderLive shows the most recent query and its results in miniature so it is
// obvious the tool is working.
function renderLive(rows, running) {
  const beat = $("liveBeat");
  const body = $("liveBody");
  if (!rows.length) {
    beat.textContent = running ? "waiting…" : "idle";
    beat.className = "beat" + (running ? " beat-on" : "");
    return;
  }
  const r = rows[0]; // newest first
  beat.textContent = running ? "live" : "stopped";
  beat.className = "beat" + (running ? " beat-on" : "");

  const items = r.preview || [];
  const cards = items.length
    ? items.map((it) => `
        <div class="mini">
          <div class="mini-title">${esc(it.title || "(no title)")}</div>
          ${it.url ? `<div class="mini-url">${esc(hostOf(it.url) || it.url)}</div>` : ""}
          ${it.snippet ? `<div class="mini-snip">${esc(it.snippet)}</div>` : ""}
        </div>`).join("")
    : `<p class="livehint">No result items to preview${r.status ? ` (HTTP ${r.status})` : ""}.</p>`;

  const notes = [];
  if (r.note) notes.push(`<div class="livenote">⚠ ${esc(r.note)}</div>`);
  if (r.assert_pass === false) notes.push(`<div class="livenote">✗ assertion failed: ${esc(r.assert_msg)}</div>`);
  const stamp = fmtTime(r.time);
  const okdot = r.ok ? `<span class="dot ok">✓</span>` : `<span class="dot bad">✗</span>`;
  const assert = assertBadge(r);

  body.innerHTML = `
    <div class="liveq">
      ${okdot}
      <span class="badge ${esc(r.kind)}">${esc(r.kind)}</span>
      <span class="liveq-text" title="${esc(r.query)}">${esc(r.query)}</span>${assert}
      <span class="liveq-meta">${r.count} results · ${r.latency_ms} ms · ${stamp}</span>
    </div>
    ${notes.join("")}
    <div class="minis">${cards}</div>`;
}

function renderResults(rows) {
  const body = $("resultsBody");
  if (!rows.length) {
    body.innerHTML = `<tr class="empty"><td colspan="6">No queries yet. Configure a mode and press Start.</td></tr>`;
    return;
  }
  body.innerHTML = rows.map((r) => {
    // Colorblind-safe: glyph + color, never color alone.
    const status = r.ok
      ? `<span class="st-ok">✓ ${r.status || "ok"}</span>`
      : `<span class="st-bad" title="${esc(r.err)}">✗ ${r.status || "err"}</span>`;
    const assert = assertBadge(r);
    return `<tr>
      <td>${fmtTime(r.time)}</td>
      <td><span class="badge ${esc(r.kind)}">${esc(r.kind)}</span></td>
      <td class="q" title="${esc(r.query)}">${esc(r.query)}${assert}</td>
      <td>${status}</td>
      <td>${r.count}</td>
      <td>${r.latency_ms}</td>
    </tr>`;
  }).join("");
}

// assertBadge returns a small PASS/FAIL chip if the row was asserted.
function assertBadge(r) {
  if (r.assert_pass === undefined || r.assert_pass === null) return "";
  return r.assert_pass
    ? ` <span class="chip chip-pass" title="assertion passed">PASS</span>`
    : ` <span class="chip chip-fail" title="${esc(r.assert_msg)}">FAIL</span>`;
}

// ---- controls ----

$("startBtn").addEventListener("click", async () => {
  const d = await (await fetch("/api/start", { method: "POST" })).json();
  if (d.error) flashCfg(d.error, true);
  refresh();
});

$("stopBtn").addEventListener("click", async () => {
  await fetch("/api/stop", { method: "POST" });
  refresh();
});

// ---- box visibility ----

function toggleBoxes() {
  const mode = $("mode").value;
  $("browserBox").classList.toggle("hidden", mode !== "browser");
  $("webBox").classList.toggle("hidden", mode !== "public-web");
  $("apiBox").classList.toggle("hidden", mode !== "official-api");

  const source = $("source").value;
  $("builtinBox").classList.toggle("hidden", source !== "builtin");
  $("csvBox").classList.toggle("hidden", source !== "csv");

  $("llmBox").classList.toggle("hidden", !$("llmEnabled").checked);
}
["mode", "source"].forEach((id) => $(id).addEventListener("change", toggleBoxes));
$("llmEnabled").addEventListener("change", toggleBoxes);

// Suggest a sensible default base URL when the provider changes.
$("llmProvider").addEventListener("change", () => {
  const cur = $("llmBaseURL").value.trim();
  if (cur === "" || cur === "http://localhost:11434" || cur === "http://localhost:1234/v1") {
    $("llmBaseURL").value = $("llmProvider").value === "openai"
      ? "http://localhost:1234/v1" : "http://localhost:11434";
  }
});

function flashCfg(msg, isErr) {
  const el = $("cfgMsg");
  el.textContent = msg;
  el.className = "msg " + (isErr ? "err" : "ok");
  if (!isErr) setTimeout(() => { el.textContent = ""; }, 3000);
}

// ---- load config into the form ----

let curConfig = null;

async function loadConfig() {
  const c = await (await fetch("/api/config")).json();
  curConfig = c;

  $("mode").value = c.mode;
  $("poll").value = c.poll_seconds;
  $("jitter").value = c.jitter_seconds;

  $("source").value = c.generation.source || "builtin";
  $("genMode").value = c.generation.mode;
  $("shuffle").checked = !!c.generation.shuffle;

  $("llmEnabled").checked = c.generation.llm.enabled;
  $("llmProvider").value = c.generation.llm.provider || "ollama";
  $("llmBaseURL").value = c.generation.llm.base_url || "";
  $("llmModel").value = c.generation.llm.model || "";
  $("llmKeyState").textContent = c.llm_key_set ? "(saved — blank keeps it)" : "";
  const kinds = c.generation.llm.kinds || {};
  $("llmTerms").checked = !!kinds.terms;
  $("llmPhrases").checked = !!kinds.phrases;
  $("llmQuestions").checked = !!kinds.questions;

  $("brURL").value = c.browser.url || "";
  $("brHeadless").checked = c.browser.headless;
  $("brItem").value = c.browser.item_selector || "";
  $("brTitle").value = c.browser.title_selector || "";
  $("brSnippet").value = c.browser.snippet_selector || "";
  $("brConsent").value = c.browser.consent_selector || "";
  $("brTimeout").value = c.browser.timeout_seconds || 30;
  $("brChrome").value = c.browser.chrome_path || "";

  $("webEndpoint").value = c.public_web.endpoint || "";
  $("webMethod").value = c.public_web.method || "GET";
  $("webParam").value = c.public_web.query_param || "";
  $("webItems").value = c.public_web.items_path || "";

  $("apiEndpoint").value = c.api.endpoint || "";
  $("apiMethod").value = c.api.method || "GET";
  $("apiParam").value = c.api.query_param || "";
  $("apiItems").value = c.api.items_path || "";
  $("keyState").textContent = c.api_key_set ? "(a key is saved — leave blank to keep it)" : "(no key saved)";

  $("appLog").checked = c.logging.app_log;
  $("csvResults").checked = c.logging.csv_results;
  $("persistMetrics").checked = c.logging.persist_metrics;

  $("assertEnabled").checked = c.assertions.enabled;
  $("assertLatency").value = c.assertions.max_latency_ms;
  $("assertMinResults").value = c.assertions.min_results;

  const al = c.alerts || {};
  $("alertEnabled").checked = al.enabled;
  $("alertWebhook").value = al.webhook_url || "";
  $("alertWindow").value = al.window_queries || 20;
  $("alertErr").value = Math.round((al.max_error_rate || 0) * 100);
  $("alertZero").value = Math.round((al.max_zero_result_rate || 0) * 100);
  $("alertAssert").value = Math.round((al.max_assert_fail_rate || 0) * 100);
  $("alertP95").value = al.max_p95_ms || 0;
  $("alertCooldown").value = al.cooldown_seconds || 300;

  toggleBoxes();
}

// ---- CSV upload ----

$("csvFile").addEventListener("change", async () => {
  const f = $("csvFile").files[0];
  if (!f) return;
  const fd = new FormData();
  fd.append("file", f);
  const d = await (await fetch("/api/terms", { method: "POST", body: fd })).json();
  if (d.error) { flashCfg("CSV: " + d.error, true); return; }
  flashCfg(`Loaded ${d.csv_count} queries.`, false);
  await loadConfig();
  $("source").value = "csv";
  toggleBoxes();
  refresh();
});

// ---- save ----

$("cfgForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  let poll = parseInt($("poll").value, 10) || 60;
  if (poll < 60) { poll = 60; $("poll").value = 60; } // mirror the server floor

  const c = curConfig || (await (await fetch("/api/config")).json());
  const payload = {
    mode: $("mode").value,
    poll_seconds: poll,
    jitter_seconds: parseInt($("jitter").value, 10) || 0,
    user_agent: c.user_agent,
    generation: {
      mode: $("genMode").value,
      source: $("source").value,
      csv_path: c.generation.csv_path, // managed by upload
      shuffle: $("shuffle").checked,
      llm: {
        enabled: $("llmEnabled").checked,
        provider: $("llmProvider").value,
        base_url: $("llmBaseURL").value.trim(),
        model: $("llmModel").value.trim(),
        api_key: $("llmKey").value, // blank preserves existing
        kinds: {
          terms: $("llmTerms").checked,
          phrases: $("llmPhrases").checked,
          questions: $("llmQuestions").checked,
        },
      },
    },
    browser: {
      url: $("brURL").value.trim(),
      headless: $("brHeadless").checked,
      item_selector: $("brItem").value.trim(),
      title_selector: $("brTitle").value.trim(),
      snippet_selector: $("brSnippet").value.trim(),
      consent_selector: $("brConsent").value.trim(),
      timeout_seconds: parseInt($("brTimeout").value, 10) || 30,
      chrome_path: $("brChrome").value.trim(),
    },
    public_web: {
      endpoint: $("webEndpoint").value.trim(),
      method: $("webMethod").value,
      query_param: $("webParam").value.trim(),
      items_path: $("webItems").value.trim(),
      title_key: c.public_web.title_key,
      url_key: c.public_web.url_key,
      snippet_key: c.public_web.snippet_key,
    },
    api: {
      endpoint: $("apiEndpoint").value.trim(),
      method: $("apiMethod").value,
      query_param: $("apiParam").value.trim(),
      key: $("apiKey").value,
      key_header: c.api.key_header,
      items_path: $("apiItems").value.trim(),
      title_key: c.api.title_key,
      url_key: c.api.url_key,
      snippet_key: c.api.snippet_key,
    },
    server: c.server,
    logging: {
      dir: c.logging.dir,
      app_log: $("appLog").checked,
      csv_results: $("csvResults").checked,
      persist_metrics: $("persistMetrics").checked,
    },
    assertions: {
      enabled: $("assertEnabled").checked,
      max_latency_ms: parseInt($("assertLatency").value, 10) || 0,
      min_results: parseInt($("assertMinResults").value, 10) || 0,
    },
    alerts: {
      enabled: $("alertEnabled").checked,
      webhook_url: $("alertWebhook").value.trim(),
      window_queries: parseInt($("alertWindow").value, 10) || 20,
      max_error_rate: (parseInt($("alertErr").value, 10) || 0) / 100,
      max_zero_result_rate: (parseInt($("alertZero").value, 10) || 0) / 100,
      max_assert_fail_rate: (parseInt($("alertAssert").value, 10) || 0) / 100,
      max_p95_ms: parseInt($("alertP95").value, 10) || 0,
      cooldown_seconds: parseInt($("alertCooldown").value, 10) || 300,
    },
  };

  const d = await (await fetch("/api/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  })).json();
  if (d.error) { flashCfg(d.error, true); return; }
  $("apiKey").value = "";
  $("llmKey").value = "";
  flashCfg("Saved.", false);
  loadConfig();
});

loadConfig();
refresh();
setInterval(refresh, 2000);
