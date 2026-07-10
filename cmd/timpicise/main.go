// Command timpicise is a standalone, cross-platform tool that exercises the
// Timpi search interface at a deliberately gentle pace. It generates search
// terms, phrases, and questions and issues at most one query per minute, with a
// local web dashboard showing results and execution metrics.
//
// By default it runs in "dry-run" mode and sends nothing over the network; the
// user opts into a live mode (public-web or official-api) from the dashboard.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/rotate"
	"github.com/mhue-ai/timpi-cise/internal/runner"
	"github.com/mhue-ai/timpi-cise/internal/server"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	// run() owns all deferred cleanup (log flush, results-CSV close). main only
	// translates its result into an exit code, so a fatal error still exits
	// non-zero *after* cleanup has run.
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	var (
		cfgPath     = flag.String("config", defaultConfigPath(), "path to config file (created if missing)")
		addr        = flag.String("addr", "", "override dashboard listen address (e.g. 127.0.0.1:8770)")
		noOpen      = flag.Bool("no-open", false, "do not open the dashboard in a browser")
		autostart   = flag.Bool("start", false, "begin polling immediately on launch")
		verbose     = flag.Bool("verbose", false, "log at debug level")
		expose      = flag.Bool("expose", false, "allow non-loopback (LAN) access to the dashboard (disables the DNS-rebinding guard)")
		showVersion = flag.Bool("version", false, "print version and exit")
	)
	flag.Parse()

	if *showVersion {
		fmt.Println("timpi-cise", version)
		return nil
	}

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		// A corrupt or unreadable config must not prevent startup. Preserve the
		// bad file for inspection and continue with defaults.
		fmt.Fprintf(os.Stderr, "config load failed (%v); backing up to %s.bad and using defaults\n", err, *cfgPath)
		if rerr := os.Rename(*cfgPath, *cfgPath+".bad"); rerr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not back up bad config: %v\n", rerr)
		}
		cfg = config.Default()
		if serr := config.Save(*cfgPath, cfg); serr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not write fresh config: %v\n", serr)
		}
	}
	addrExplicit := *addr != ""
	if addrExplicit {
		cfg.Server.Addr = *addr
	}
	cfg.Sanitize()

	// Zero-arguments friendliness: if the default port is already taken (e.g. a
	// stray instance is still running), pick the next free port automatically so
	// a plain double-click just works instead of failing to bind. An explicit
	// --addr is respected exactly (no auto-shifting).
	if !addrExplicit {
		cfg.Server.Addr = firstFreeAddr(cfg.Server.Addr)
	}

	logger, closeLog := setupLogging(cfg, *verbose)
	defer closeLog()

	// Anti-DNS-rebinding: LAN access is only allowed when the user explicitly
	// binds to a non-loopback address AND passes --expose.
	bindLocal := hostIsLoopback(cfg.Server.Addr)
	allowLAN := false
	if !bindLocal {
		if *expose {
			allowLAN = true
			logger.Warn("dashboard exposed to the network", "addr", cfg.Server.Addr)
		} else {
			logger.Warn("bound to a non-loopback address without --expose; the DNS-rebinding guard will reject non-local requests. Pass --expose to allow LAN access.", "addr", cfg.Server.Addr)
		}
	}

	// Counters reset on every start (they are not restored from disk), so each
	// run reports fresh metrics. Persistence, if enabled, still writes a live
	// snapshot to metrics.json for external scraping/backup.
	met := metrics.New(50)
	run := runner.New(cfg, *cfgPath, met, logger)
	defer run.Close()
	srv := server.New(run, met, logger, version, allowLAN)

	logger.Info("timpi-cise starting", "version", version, "mode", cfg.Mode, "addr", cfg.Server.Addr, "log_dir", cfg.Logging.Dir)

	if *autostart {
		if err := run.Start(); err != nil {
			logger.Warn("autostart skipped", "err", err)
		}
	}

	url := "http://" + normalizeHost(srv.Addr())
	fmt.Println()
	fmt.Println("  timpi-cise is running.")
	fmt.Println()
	fmt.Println("  Dashboard:  " + url)
	if !*noOpen {
		fmt.Println("              (opening it in your browser now...)")
	}
	if !*autostart {
		fmt.Println("  Next step:  press the \"Start\" button on the dashboard.")
	}
	fmt.Println("  Mode:       " + cfg.Mode + " (at most one search per minute)")
	fmt.Println()
	fmt.Println("  To stop:    close this window (or press Ctrl+C).")
	fmt.Println()

	if !*noOpen {
		go openBrowserWhenReady(srv.Addr(), url)
	}

	// Graceful shutdown on signal or on a fatal server error. Using a channel
	// (rather than os.Exit inside the goroutine) ensures the deferred cleanup —
	// run.Close() and closeLog() — always runs so logs and the results CSV are
	// flushed.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		if err := srv.Start(); err != nil {
			serverErr <- err
		}
	}()

	// Periodically persist metrics so a crash loses at most one interval.
	if cfg.Logging.PersistMetrics {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					if err := met.SaveTo(cfg.MetricsPath()); err != nil {
						logger.Warn("metrics save failed", "err", err)
					}
				}
			}
		}()
	}

	var fatal error
	select {
	case <-ctx.Done():
		fmt.Println("\nshutting down…")
	case err := <-serverErr:
		logger.Error("dashboard server failed", "addr", cfg.Server.Addr, "err", err)
		fatal = err
	}

	if cfg.Logging.PersistMetrics {
		if err := met.SaveTo(cfg.MetricsPath()); err != nil {
			logger.Warn("final metrics save failed", "err", err)
		}
	}
	run.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("server shutdown did not complete cleanly", "err", err)
	}
	return fatal
}

// setupLogging builds a slog.Logger that writes to stderr and, if enabled, to a
// log file under the configured log directory. It returns a close function that
// flushes/closes the file.
func setupLogging(cfg config.Config, verbose bool) (*slog.Logger, func()) {
	level := slog.LevelInfo
	if verbose {
		level = slog.LevelDebug
	}
	writers := []io.Writer{os.Stderr}
	closeFn := func() {}

	if cfg.Logging.AppLog {
		if f, ferr := rotate.New(cfg.AppLogPath(), 10<<20); ferr != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot open log file %q: %v (logging to terminal only)\n", cfg.AppLogPath(), ferr)
		} else {
			writers = append(writers, f)
			closeFn = func() { _ = f.Close() }
		}
	}

	h := slog.NewTextHandler(io.MultiWriter(writers...), &slog.HandlerOptions{Level: level})
	logger := slog.New(h)
	slog.SetDefault(logger)
	return logger, closeFn
}

// defaultConfigPath places the config next to the executable's working dir under
// a stable, per-user location.
func defaultConfigPath() string {
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "timpi-cise", "config.json")
	}
	return "timpi-cise-config.json"
}

// firstFreeAddr returns the given host:port if it can be bound, otherwise the
// next free port after it (searching a small range). If none is free it returns
// the original address and lets the server report the bind error.
func firstFreeAddr(addr string) string {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return addr
	}
	for i := 0; i < 20; i++ {
		cand := net.JoinHostPort(host, strconv.Itoa(port+i))
		ln, err := net.Listen("tcp", cand)
		if err == nil {
			_ = ln.Close()
			return cand
		}
	}
	return addr
}

// hostIsLoopback reports whether a listen address binds only to the loopback
// interface (or an empty host, which Go treats as all interfaces → not local).
func hostIsLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// normalizeHost turns a listen address like ":8770" or "0.0.0.0:8770" into
// something a browser can open.
func normalizeHost(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// openBrowserWhenReady waits until the dashboard accepts connections, then opens
// it in the default browser.
func openBrowserWhenReady(addr, url string) {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", normalizeHost(addr), 300*time.Millisecond)
		if err == nil {
			c.Close()
			openBrowser(url)
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	if err := exec.Command(cmd, args...).Start(); err != nil {
		// Non-fatal: the user can open the URL manually.
		slog.Debug("could not open browser", "url", url, "err", err)
	}
}

// ensure config dir exists is handled by config.Save via os.WriteFile failing
// gracefully; create the directory here for the default path.
func init() {
	if dir, err := os.UserConfigDir(); err == nil {
		_ = os.MkdirAll(filepath.Join(dir, "timpi-cise"), 0o755)
	}
}
