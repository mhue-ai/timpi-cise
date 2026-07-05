// Dashboard front-end. Polls /api/status, renders metrics + recent results, and
// drives the start/stop and configuration endpoints. No external dependencies.

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
  const d = new Date(iso);
  return d.toLocaleTimeString();
}

function esc(s) {
  return String(s ?? "").replace(/[&<>"]/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" }[c]));
}

async function refresh() {
  try {
    const r = await fetch("/api/status");
    const d = await r.json();
    const m = d.metrics;

    $("mSent").textContent = m.sent;
    $("mOK").textContent = m.ok;
    $("mFail").textContent = m.failed;
    $("mLat").textContent = m.sent ? `${m.avg_latency_ms} ms` : "—";
    $("mUp").textContent = fmtDuration(m.uptime_seconds);
    $("mMode").textContent = `${d.mode} · ${d.adapter}`;

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

    renderResults(m.recent || []);
  } catch (e) {
    // Server not reachable yet; ignore.
  }
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
  const r = await fetch("/api/start", { method: "POST" });
  const d = await r.json();
  if (d.error) flashCfg(d.error, true);
  refresh();
});

$("stopBtn").addEventListener("click", async () => {
  await fetch("/api/stop", { method: "POST" });
  refresh();
});

// ---- config form ----

function toggleBoxes() {
  const mode = $("mode").value;
  $("webBox").classList.toggle("hidden", mode !== "public-web");
  $("apiBox").classList.toggle("hidden", mode !== "official-api");
  $("ollamaBox").classList.toggle("hidden", !$("useOllama").checked);
}
$("mode").addEventListener("change", toggleBoxes);
$("useOllama").addEventListener("change", toggleBoxes);

function flashCfg(msg, isErr) {
  const el = $("cfgMsg");
  el.textContent = msg;
  el.className = "msg " + (isErr ? "err" : "ok");
  if (!isErr) setTimeout(() => { el.textContent = ""; }, 3000);
}

async function loadConfig() {
  const r = await fetch("/api/config");
  const c = await r.json();
  $("mode").value = c.mode;
  $("poll").value = c.poll_seconds;
  $("jitter").value = c.jitter_seconds;
  $("genMode").value = c.generation.mode;
  $("useOllama").checked = c.generation.use_ollama;
  $("ollamaURL").value = c.generation.ollama_url || "";
  $("ollamaModel").value = c.generation.ollama_model || "";

  $("webEndpoint").value = c.public_web.endpoint || "";
  $("webMethod").value = c.public_web.method || "GET";
  $("webParam").value = c.public_web.query_param || "";
  $("webItems").value = c.public_web.items_path || "";

  $("apiEndpoint").value = c.api.endpoint || "";
  $("apiMethod").value = c.api.method || "GET";
  $("apiParam").value = c.api.query_param || "";
  $("apiItems").value = c.api.items_path || "";
  $("keyState").textContent = c.api_key_set ? "(a key is saved — leave blank to keep it)" : "(no key saved)";

  toggleBoxes();
}

$("cfgForm").addEventListener("submit", async (e) => {
  e.preventDefault();
  // Client-side guard mirrors the server's hard floor.
  let poll = parseInt($("poll").value, 10) || 60;
  if (poll < 60) { poll = 60; $("poll").value = 60; }

  const cur = await (await fetch("/api/config")).json();
  const payload = {
    mode: $("mode").value,
    poll_seconds: poll,
    jitter_seconds: parseInt($("jitter").value, 10) || 0,
    user_agent: cur.user_agent,
    generation: {
      mode: $("genMode").value,
      use_ollama: $("useOllama").checked,
      ollama_url: $("ollamaURL").value.trim(),
      ollama_model: $("ollamaModel").value.trim(),
    },
    public_web: {
      endpoint: $("webEndpoint").value.trim(),
      method: $("webMethod").value,
      query_param: $("webParam").value.trim(),
      items_path: $("webItems").value.trim(),
      title_key: cur.public_web.title_key,
      url_key: cur.public_web.url_key,
      snippet_key: cur.public_web.snippet_key,
    },
    api: {
      endpoint: $("apiEndpoint").value.trim(),
      method: $("apiMethod").value,
      query_param: $("apiParam").value.trim(),
      key: $("apiKey").value, // blank preserves existing on the server
      key_header: cur.api.key_header,
      items_path: $("apiItems").value.trim(),
      title_key: cur.api.title_key,
      url_key: cur.api.url_key,
      snippet_key: cur.api.snippet_key,
    },
    server: cur.server,
  };

  const r = await fetch("/api/config", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(payload),
  });
  const d = await r.json();
  if (d.error) { flashCfg(d.error, true); return; }
  $("apiKey").value = "";
  flashCfg("Saved.", false);
  loadConfig();
});

loadConfig();
refresh();
setInterval(refresh, 2000);
