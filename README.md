# timpi-cise

A small, self-contained tool that **exercises the [Timpi](https://timpi.io) search
interface** at a deliberately gentle pace and shows results plus live execution
metrics in a local dashboard.

It generates search **terms**, **phrases**, and **questions** and issues them one
at a time, **never more than once per minute**. It ships as a single executable
for Windows, Linux, macOS, and Raspberry Pi — no Docker, no runtime, no dev
environment required.

> **Responsible use.** This tool is meant to gently exercise / smoke-test the
> search interface, not to generate abusive traffic. The one-query-per-minute
> floor is compiled in and cannot be lowered. Please only point it at services
> you are authorized to test. See [Safety & anti-abuse](#safety--anti-abuse).

---

## Quick start

1. Download the binary for your platform from `dist/` (or build it — see below).
2. Run it:

   ```bash
   # Linux / macOS / Raspberry Pi
   ./timpicise-linux-amd64

   # Windows (double-click, or from a terminal)
   timpicise-windows-amd64.exe
   ```

3. Your browser opens the dashboard at `http://127.0.0.1:8770`.
4. It starts in **dry-run** mode (no network). Pick a mode, press **Start**, and
   watch the **Live results** panel — it shows each query and its top results in
   miniature (title, host, snippet) with a pulsing "LIVE" indicator, so you can
   see at a glance that it's working. If an endpoint returns HTML instead of
   results, an honest amber note says so rather than faking success.

### Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | per-user config dir | Config file (created if missing). |
| `--addr <host:port>` | `127.0.0.1:8770` | Dashboard listen address (loopback). |
| `--no-open` | off | Don't auto-open a browser. |
| `--start` | off | Begin polling immediately on launch. |
| `--verbose` | off | Log at debug level. |
| `--expose` | off | Allow non-loopback (LAN) access (disables the DNS-rebinding guard). |
| `--version` | — | Print version and exit. |

---

## Modes

| Mode | What it does | Needs |
|------|--------------|-------|
| **dry-run** (default) | Generates queries and drives the whole pipeline **without any network activity**. Safe for demos and testing. | nothing |
| **public-web** | Exercises the public `timpi.com` search the way the site itself does. | the search request URL (see below) |
| **official-api** | Uses an authenticated Timpi Data API endpoint. | endpoint URL + API key |

### The destination endpoint (what's best)

The **endpoint is fully user-editable** in the dashboard, and the app ships with
the best discoverable default already filled in. Here's what the default is and
why:

`timpi.com`'s search is a **Blazor Server** application. It runs over a stateful
**SignalR WebSocket** (`/_blazor`) with server-rendered UI diffs — there is **no
public REST/JSON search endpoint** to point an HTTP client at. (`api.timpi.com`
exists but 404s on search paths; `timpi.com/api/search?q=…` just returns the
app's HTML shell.)

So:

- **For real, robust programmatic exercising, use `official-api` mode** with the
  Timpi **Data API** endpoint + key you request from `hello@timpi.io`. That is
  the interface Timpi actually provides for machine queries.
- The **`public-web` default** (`https://timpi.com/api/search?q={query}`) is a
  best-effort HTTP starting point. Because timpi.com currently returns HTML there
  rather than JSON, **the app tells you so honestly** — it shows an amber note
  *"endpoint returned an HTML page, not JSON results"* instead of faking success.
  Override the field with a real REST endpoint whenever one becomes available.
- To point at any JSON search API: put the URL in **Endpoint URL** (use `{query}`
  for the term, or set a **Query param** like `q`), then set **Results JSON path**
  (e.g. `data.results`) plus the title/url/snippet field names so results parse
  and show in the live preview.

> **Faithful human-UI exercising** (driving the actual Blazor page in a real
> browser) is out of scope for the self-contained binary — it would require
> bundling a headless browser. The Data API is the intended machine path.

### Configuring `official-api`

As a Timpi stakeholder you can request a Data API endpoint + key from
`hello@timpi.io`. Enter the endpoint (with `{query}` or a query param) and your
key. The key is stored locally and is **never sent to the browser** by the
dashboard.

---

## Query sources

You choose where queries come from:

### Built-in generator

- **terms** — short generic searches (e.g. `renewable energy`, `chess strategy tips`).
- **phrases** — multi-word phrases from templates (e.g. `affordable electric vehicles for beginners`).
- **questions** — natural-language questions (e.g. `how does quantum computing actually work`).
- **mixed** — rotates through all three.

### Your own CSV term list

Select **"my CSV term list"** and upload a `.csv`/`.txt` file (one query per
line). An optional second column sets the kind (`terms`/`phrases`/`questions`);
otherwise the kind is inferred from the text. Tick **Shuffle** to randomize the
order. The uploaded list is saved under the log folder and reused across runs.

```csv
best privacy tools
how does decentralized search work?,questions
solar panel payback period,phrases
```

### Optional: model server for any/all types

Enable **"Use a local model server for advanced questions"** to have a model
generate queries. It can produce **all three types** — short terms, long
phrases, and questions — and you pick per type whether it comes from the
**model** or the **built-in CPU generator**:

| Type | Built-in (CPU) | Model server |
|------|:--------------:|:------------:|
| short terms | ✅ | ✅ (tick "short terms") |
| long phrases | ✅ | ✅ (tick "long phrases") |
| questions | ✅ | ✅ (tick "questions") |

Any type left unticked uses the CPU generator. If the server is unreachable,
**every** type falls back to CPU automatically — so this stays optional and the
binary stays tiny (no model is ever bundled).

Two providers are supported:

- **Ollama (native)** — [ollama.com](https://ollama.com); default `http://localhost:11434`.
  Uses your GPU if one is attached, else CPU.
- **OpenAI-compatible** — any server exposing `/v1/chat/completions`, including
  **LM Studio, llama.cpp's server, Jan, LocalAI, vLLM, text-generation-webui**,
  or a hosted API. Default base URL `http://localhost:1234/v1`. An API key field
  is available for servers that require one.

## Assertions & golden queries (monitor mode)

Beyond generating traffic, timpi-cise can **check** each query and report
pass/fail — turning it into a lightweight search monitor.

- **Global assertions** (dashboard → *assertions*): fail a query if it errors,
  returns fewer than *N* results, or exceeds a *max latency*.
- **Golden queries**: add a **third column** to your CSV with a substring that a
  result must contain. Example `queries.csv`:

  ```csv
  timpi,terms,timpi.io
  privacy tools,phrases,
  how does search indexing work?,questions,
  ```

  The first row asserts that searching `timpi` returns a result containing
  `timpi.io` — a classic index-regression canary. Golden checks run even with
  global assertions off.

Failures are counted (dashboard **Assertions** card), flagged per row (**FAIL**
chip), logged (`assertion failed …`), and recorded in the results CSV.

## Metrics & monitoring

- **Latency percentiles** — p50 / p95 / p99 (averages hide tail latency).
- **Zero-result rate** — the share of successful queries returning nothing, a
  key search-health signal.
- **Trends** — per-minute sparklines of average latency and success rate.
- **`/healthz`** — JSON liveness (`status`, `version`, `uptime`, `running`).
- **`/metrics`** — Prometheus exposition format for Grafana/alerting, e.g.
  `timpicise_queries_total`, `timpicise_zero_results_total`,
  `timpicise_assert_failures_total`, `timpicise_latency_ms_p95`.
- **`--version`** — prints the embedded build version.

## Logging

- **CSV results log** — every executed query is appended to `results.csv`
  (time, mode, kind, query, status, count, latency, ok, error, top title).
  Download it any time from the dashboard, or open the file directly.
- **App log** — a structured `timpicise.log` capturing lifecycle, config
  changes, and errors (also echoed to the terminal). Run with `--verbose` for
  debug-level detail.
- Both live under a per-user **log folder** shown in the dashboard (overridable
  via the config file). Both can be toggled off.

---

## Safety & anti-abuse

- **Hard floor of 1 query / 60s.** Enforced in code (`config.MinPollSeconds`);
  the UI and config file cannot go below it.
- **One query at a time.** No concurrency, no burst.
- **Randomized jitter** so traffic isn't perfectly periodic.
- **Honest User-Agent** identifying the tool and its repo.
- **Backoff that honors `429`/`503`** and `Retry-After` — it slows down when the
  server asks it to.
- **Dry-run by default** — it does nothing over the network until you opt in.
- **Loopback dashboard** — the UI binds to `127.0.0.1` by default.
- **DNS-rebinding / CSRF guard** — the dashboard rejects requests with a
  non-local `Host` header and cross-origin state-changing requests. Binding to a
  non-loopback address requires an explicit `--expose` flag (with a warning).
- **Endpoint URL validation** — only `http(s)` endpoints are accepted.
- **Log & CSV rotation** — the app log and results CSV rotate at 10 MiB so they
  can't grow without bound.

---

## Build from source

Requires [Go](https://go.dev/dl/) 1.26+.

```bash
# Build for your current platform
go build -o timpicise ./cmd/timpicise

# Run the tests
go test ./...

# Cross-compile every supported target into ./dist
./build.sh
```

`build.sh` produces binaries for Windows (amd64/arm64), Linux (amd64/arm64),
Raspberry Pi (armv7 32-bit, armv6 Pi Zero), and macOS (amd64/arm64) — all from a
single machine, thanks to Go's cross-compilation.

---

## Project layout

```
cmd/timpicise/        entry point (flags, browser open, shutdown)
internal/config/      configuration + safety invariants (60s floor)
internal/generate/    generators + CSV source + model clients (ollama, openai)
internal/search/      adapters: dry-run, public-web, official-api
internal/runner/      the rate-limited polling loop + backoff
internal/metrics/     counters, latency percentiles, per-minute time series
internal/reslog/      CSV results-log writer (with rotation)
internal/rotate/      size-based rotating file writer (app log)
internal/server/      local dashboard + JSON API + /healthz + /metrics + guard
```

## License

[MIT](LICENSE).
