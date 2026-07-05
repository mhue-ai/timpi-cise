# timpi-cise — technical reference

Full detail on every mode, setting, metric, and endpoint. For a plain-English
overview and install steps, see the [README](README.md).

## Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | per-user config dir | Config file (created if missing). |
| `--addr <host:port>` | `127.0.0.1:8770` | Dashboard listen address (loopback). |
| `--no-open` | off | Don't auto-open a browser. |
| `--start` | off | Begin polling immediately on launch. |
| `--verbose` | off | Log at debug level. |
| `--expose` | off | Allow non-loopback (LAN) access (disables the DNS-rebinding guard). |
| `--version` | — | Print version and exit. |

## Connection modes

| Mode | What it does | Needs |
|------|--------------|-------|
| **dry-run** (default) | Generates queries and drives the whole pipeline with **no network activity**. | nothing |
| **browser** | Drives the real `timpi.com` UI in a headless browser and scrapes the rendered results. The faithful way to exercise timpi.com. | an installed Chrome/Edge/Chromium |
| **public-web** | Hits a REST/JSON search endpoint over plain HTTP. | a REST endpoint |
| **official-api** | Uses an authenticated Timpi Data API endpoint. | endpoint URL + API key |

### Why browser mode exists

`timpi.com` is a **Blazor Server** app — its search runs client-side over a
SignalR WebSocket, so there is no REST endpoint that returns JSON. Browser mode
launches an installed **Chrome/Edge/Chromium** (auto-detected; override with a
path), navigates to `https://timpi.com/search?q={query}`, dismisses the cookie
banner, waits for results to render, and scrapes them. Defaults target
timpi.com's DOM (`.all-item-content` / `a.title` / `.description`); every
selector is editable for other sites. It's heavier (~a few seconds/query) but is
the only way to get **real** results from a client-rendered search site. The
1-query/minute floor still applies; the browser is not bundled.

### public-web / official-api

Put the URL in **Endpoint URL** (use `{query}` for the term, or set a **Query
param** like `q`) and a **Results JSON path** (e.g. `data.results`) plus the
title/url/snippet field names. If an endpoint returns HTML instead of JSON, the
app shows an honest "returned an HTML page, not JSON results" note rather than
faking success. The official-api key is stored locally and never sent to the
browser.

## Query sources

### Built-in generator

- **realistic** — curated corpus of real-world queries across navigational /
  informational / transactional / local intents, sampled head-weighted
  (Zipfian-like) so common queries recur and the long tail stays diverse.
- **terms / phrases / questions** — short terms, multi-word phrases, or
  natural-language questions.
- **mixed** — rotates through terms, phrases, and questions.

### Your own CSV term list

Upload a `.csv`/`.txt` file (one query per line). Optional 2nd column sets the
kind (`terms`/`phrases`/`questions`); optional 3rd column is a **golden-query**
substring a result must contain:

```csv
timpi,terms,timpi.io
best budget laptop,phrases,
how does search indexing work?,questions,
```

### Optional model server (local GPU or remote)

Have a model generate queries via **Ollama** or any **OpenAI-compatible** server
(LM Studio, llama.cpp, Jan, LocalAI, vLLM, text-generation-webui, or a hosted
API). Choose per type — short terms / long phrases / questions — whether it comes
from the model or the built-in CPU generator. If the server is unreachable, every
type falls back to CPU. No model is bundled.

## Assertions & golden queries (monitor mode)

- **Global assertions**: fail a query if it errors, returns fewer than *N*
  results, or exceeds a max latency.
- **Golden queries**: the 3rd CSV column asserts a result must contain a given
  substring — an index-regression canary, checked even with global assertions off.

Failures are counted, badged (**FAIL** chip), logged, and written to the CSV.

## Metrics & monitoring

- **Latency percentiles** — p50 / p95 / p99.
- **Zero-result rate** — share of successful queries returning nothing.
- **Trends** — per-minute sparklines of average latency and success rate.
- **Persistence** — counters/trends saved to `<logdir>/metrics.json` every 30s
  and on shutdown, restored on startup.
- **`/healthz`** — JSON liveness (`status`, `version`, `uptime`, `running`).
- **`/metrics`** — Prometheus exposition format for Grafana/alerting, e.g.
  `timpicise_queries_total`, `timpicise_zero_results_total`,
  `timpicise_assert_failures_total`, `timpicise_latency_ms_p95`.

## Alerts

Thresholds evaluated over the last *N* queries: **error rate**, **zero-result
rate**, **assertion-failure rate**, **p95 latency**. On a breach the tool logs an
error, shows a dashboard banner, and (if a **webhook URL** is set) POSTs a
message — compatible with **Slack** (`text`), **Discord** (`content`), and generic
incoming webhooks. Edge-triggered with a cooldown; sends a recovery notice when
metrics return to normal.

## Logging

- **CSV results log** — `results.csv`: every query (time, mode, kind, query,
  status, count, latency, ok, assert, note, error, top title). Downloadable from
  the dashboard. Rotates at 10 MiB (rotation never truncates on a failed rename).
- **App log** — `timpicise.log`: structured lifecycle/error log (also to the
  terminal; `--verbose` for debug). Rotates at 10 MiB.
- Both live under a per-user **log folder** shown in the dashboard; each is
  toggleable.

## Safety & anti-abuse

- **Hard floor of 1 query / 60s** — compiled in (`config.MinPollSeconds`); the UI
  and config file cannot go below it.
- **One query at a time.** No concurrency, no burst. Randomized jitter.
- **Honest User-Agent** identifying the tool and its repo.
- **Backoff that honors `429`/`503`** and `Retry-After`.
- **Dry-run by default** — does nothing over the network until you opt in.
- **Loopback dashboard** — binds to `127.0.0.1` by default.
- **DNS-rebinding / CSRF guard** — rejects requests with a non-local `Host`
  header and cross-origin state-changing requests. Non-loopback bind requires
  `--expose`.
- **Endpoint URL validation** — only `http(s)` endpoints accepted.

## Accessibility

Skip link, visible keyboard focus states, ARIA roles/labels (`role=alert` /
`aria-live` for alerts and live results, scoped table headers, labelled charts),
colorblind-safe status glyphs (✓/✗, not color alone), and `prefers-reduced-motion`.

## Build from source

Requires [Go](https://go.dev/dl/) 1.26+.

```bash
go build -o timpicise ./cmd/timpicise   # current platform
go test ./...                           # run the tests
./build.sh                              # cross-compile every target into ./dist
```

The only third-party dependency is [`chromedp`](https://github.com/chromedp/chromedp)
(pure Go), used for browser mode; it drives an installed browser and bundles
nothing, so the single binary still cross-compiles everywhere.

`build.sh` produces binaries for Windows (amd64/arm64), Linux (amd64/arm64),
Raspberry Pi (armv7 32-bit, armv6 Pi Zero), and macOS (amd64/arm64).

## Project layout

```
cmd/timpicise/        entry point (flags, browser open, shutdown, persistence)
internal/config/      configuration + safety invariants (60s floor)
internal/generate/    generators + realistic corpus + CSV source + LLM clients
internal/search/      adapters: dry-run, browser (chromedp), public-web, official-api
internal/runner/      the rate-limited polling loop + backoff + assertions
internal/metrics/     counters, percentiles, time series, persistence, windows
internal/alert/       threshold evaluation + webhook notifications
internal/reslog/      CSV results-log writer (with safe rotation)
internal/rotate/      size-based rotating file writer (app log)
internal/server/      local dashboard + JSON API + /healthz + /metrics + guard
```
