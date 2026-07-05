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
    $("mLat").textContent = m.sent ? `${m.avg_latency_ms} ms` : "—";
    $("mUp").textContent = fmtDuration(m.uptime_seconds);
    $("mMode").textContent = `${d.mode} · ${d.adapter}`;

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

  const note = r.note ? `<div class="livenote">⚠ ${esc(r.note)}</div>` : "";
  const stamp = fmtTime(r.time);
  const okdot = r.ok ? `<span class="dot ok"></span>` : `<span class="dot bad"></span>`;

  body.innerHTML = `
    <div class="liveq">
      ${okdot}
      <span class="badge ${esc(r.kind)}">${esc(r.kind)}</span>
      <span class="liveq-text" title="${esc(r.query)}">${esc(r.query)}</span>
      <span class="liveq-meta">${r.count} results · ${r.latency_ms} ms · ${stamp}</span>
    </div>
    ${note}
    <div class="minis">${cards}</div>`;
}

function renderResults(rows) {
  const body = $("resultsBody");
  if (!rows.length) {
    body.innerHTML = `<tr class="empty"><td colspan="6">No queries yet. Configure a mode and press Start.</td></tr>`;
    return;
  }
  body.innerHTML = rows.map((r) => {
    const status = r.ok
      ? `<span class="st-ok">${r.status || "ok"}</span>`
      : `<span class="st-bad" title="${esc(r.err)}">${r.status || "err"}</span>`;
    return `<tr>
      <td>${fmtTime(r.time)}</td>
      <td><span class="badge ${esc(r.kind)}">${esc(r.kind)}</span></td>
      <td class="q" title="${esc(r.query)}">${esc(r.query)}</td>
      <td>${status}</td>
      <td>${r.count}</td>
      <td>${r.latency_ms}</td>
    </tr>`;
  }).join("");
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
