# timpi-cise

**A tiny app that runs real searches on [Timpi](https://timpi.com) and shows you — live, in your browser — that it's working.**

You download one file, double-click it, and a dashboard opens showing each search and how it's doing. That's it. No accounts, no Docker, no setup.

---

## What is this, in plain English?

[Timpi](https://timpi.io) is a **private, decentralized search engine** — an alternative to Google that doesn't track you.

**timpi-cise** is a small program you run on your own computer. It:

1. **Makes up realistic search queries** (like "best budget laptop" or "how does compost work").
2. **Searches Timpi with them** — gently, at most **one search per minute**.
3. **Shows you the results and health stats** on a live dashboard in your web browser.

Think of it as a **friendly heartbeat monitor** for Timpi search: it exercises the search, watches how fast and how well it responds, and tells you the moment something looks off.

### Who is it for?

- **Timpi supporters** who want to help exercise the search and see it working.
- **Anyone** who wants a simple, visual way to check that a search service is healthy over time.

> **It's built to be gentle, not abusive.** It never sends more than one search per minute — that limit is baked into the program and can't be turned up. See [Is it safe?](#is-it-safe) below.

---

## Install

### The easy way — download and run

1. Go to the [**Releases page**](https://github.com/mhue-ai/timpi-cise/releases/latest).
2. Download the file for your computer:

   | Your computer | File to download |
   |---|---|
   | **Windows** | `timpicise-windows-amd64.exe` |
   | **Mac** (Apple Silicon — M1/M2/M3) | `timpicise-darwin-arm64` |
   | **Mac** (Intel) | `timpicise-darwin-amd64` |
   | **Linux** | `timpicise-linux-amd64` |
   | **Raspberry Pi** (64-bit) | `timpicise-linux-arm64` |
   | **Raspberry Pi** (32-bit / Pi Zero) | `timpicise-linux-armv7` / `timpicise-linux-armv6` |

3. **Run it:**
   - **Windows:** double-click the `.exe`. (If Windows shows a warning, click *More info → Run anyway* — the file isn't code-signed yet.)
   - **Mac / Linux / Raspberry Pi:** open a terminal in the download folder and run:
     ```bash
     chmod +x timpicise-*        # make it runnable (first time only)
     ./timpicise-linux-amd64     # use your file's name
     ```

Your web browser opens automatically at **http://127.0.0.1:8770** with the dashboard. Done.

### Build it yourself (optional)

If you'd rather build from source, you only need [Go](https://go.dev/dl/) 1.26+:

```bash
git clone https://github.com/mhue-ai/timpi-cise.git
cd timpi-cise
go build -o timpicise ./cmd/timpicise
./timpicise
```

To build for every platform at once: `./build.sh` (outputs to `dist/`).

---

## How to use it

1. **Run the app** — the dashboard opens in your browser.
2. By default it's set to **browser mode** — real searches against **timpi.com** — using a real browser in the background. *(This needs Chrome, Edge, or Chromium installed; most computers already have one.)* Nothing is sent until you press Start. If you'd rather practice safely first, switch the mode to **dry-run** (which sends nothing anywhere).
3. Press **Start** and watch the **Live results** panel fill in.
4. *(Optional)* If you run **LM Studio** or **Ollama**, the app is set up to have your local model write the search questions — click **Fetch installed models** and pick one. Don't have one? No problem — it falls back to its own built-in question generator automatically.

The dashboard shows each search, the results it found, how fast it was, and overall health (success rate, slowness, and more). Counters reset each time you start the app.

---

## Is it safe?

Yes — it's deliberately built to be a gentle, good-neighbor tool, not a way to hammer a service:

- 🐢 **One search per minute, maximum.** This limit is compiled into the program and **cannot be increased**.
- 🔒 **Runs only on your computer.** The dashboard is private to your machine unless you explicitly open it up.
- 🙅 **Never searches until you say so.** Polling doesn't start on its own — you press **Start**. And it tells you clearly when a search returns no real results instead of pretending everything's fine.
- 🛑 **Backs off automatically** if the service ever asks it to slow down.

---

## A quick tour of the features

- **Live dashboard** — see every search and its results as they happen.
- **Realistic queries** — a built-in library of real-world searches, or upload your own list.
- **Optional local AI** — if you run Ollama or LM Studio, it can generate the search questions with your own model (falls back to the built-in generator if not).
- **Health metrics** — success rate, zero-result rate, response-time percentiles, and hour-long trend charts.
- **Checks & alerts** — set expectations (e.g. "a search should return at least 3 results in under 2 seconds") and get a notification (Slack/Discord/webhook) if things go wrong.
- **Keeps history** — stats survive restarts, and every search is saved to a spreadsheet-friendly CSV file you can download.
- **Runs everywhere** — Windows, Mac, Linux, and Raspberry Pi, all from a single small file.

---

## Questions?

- **Does it cost anything?** No. It's free and open source (MIT license).
- **Do I need a Timpi account or API key?** No — browser mode uses the public timpi.com site. (An optional "official API" mode exists for advanced users with a Timpi Data API key.)
- **Where are my logs saved?** In a per-user folder shown on the dashboard (the *Logging* section).
- **Something looks wrong / I have an idea.** Please [open an issue](https://github.com/mhue-ai/timpi-cise/issues).

For the full technical details — every mode, setting, metric, and endpoint — see [**DETAILS.md**](DETAILS.md).

## License

[MIT](LICENSE) © mhue-ai
