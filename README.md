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
   watch the metrics.

### Command-line flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config <path>` | per-user config dir | Config file (created if missing). |
| `--addr <host:port>` | `127.0.0.1:8770` | Dashboard listen address (loopback). |
| `--no-open` | off | Don't auto-open a browser. |
| `--start` | off | Begin polling immediately on launch. |

---

## Modes

| Mode | What it does | Needs |
|------|--------------|-------|
| **dry-run** (default) | Generates queries and drives the whole pipeline **without any network activity**. Safe for demos and testing. | nothing |
| **public-web** | Exercises the public `timpi.com` search the way the site itself does. | the search request URL (see below) |
| **official-api** | Uses an authenticated Timpi Data API endpoint. | endpoint URL + API key |

### Configuring `public-web`

`timpi.com` is a JavaScript app whose results load from a background request that
isn't publicly documented, so the endpoint is **configuration**, not hardcoded.
Capture it once:

1. Open `https://timpi.com` in Chrome/Firefox.
2. Open **DevTools → Network** (F12), then run a search.
3. Find the request that returns the results (usually JSON).
4. Copy its URL into the dashboard's **Endpoint URL** field. Use `{query}` where
   the search term goes, e.g. `https://…/api/search?q={query}`, or leave a
   **Query param** like `q` and the tool appends it.
5. (Optional) Set **Results JSON path** (e.g. `data.results`) plus the title/url/
   snippet field names so parsed results show in the table.

### Configuring `official-api`

As a Timpi stakeholder you can request a Data API endpoint + key from
`hello@timpi.io`. Enter the endpoint (with `{query}` or a query param) and your
key. The key is stored locally and is **never sent to the browser** by the
dashboard.

---

## Query generation

- **terms** — short generic searches (e.g. `renewable energy`, `chess strategy tips`).
- **phrases** — multi-word phrases from templates (e.g. `affordable electric vehicles for beginners`).
- **questions** — natural-language questions (e.g. `how does quantum computing actually work`).
- **mixed** — rotates through all three.

### Optional: local GPU (Ollama) for advanced questions

Enable **"Use local GPU (Ollama) for advanced questions"** to generate richer,
more varied questions on your own machine via [Ollama](https://ollama.com):

1. Install Ollama and pull a small model, e.g. `ollama pull llama3.2`.
2. Make sure `ollama serve` is running (default `http://localhost:11434`).
3. Tick the box in the dashboard and set the model name.

If Ollama isn't reachable, the tool silently falls back to the built-in
template generator — so this stays optional and the binary stays tiny (the model
is never bundled).

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

---

## Build from source

Requires [Go](https://go.dev/dl/) 1.26+.

```bash
# Build for your current platform
go build -o timpicise ./cmd/timpicise

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
internal/generate/    term / phrase / question generators + optional Ollama
internal/search/      adapters: dry-run, public-web, official-api
internal/runner/      the rate-limited polling loop + backoff
internal/metrics/     thread-safe counters + recent-results buffer
internal/server/      local dashboard (embedded HTML/CSS/JS + JSON API)
```

## License

[MIT](LICENSE).
