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
	"syscall"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
	"github.com/mhue-ai/timpi-cise/internal/runner"
	"github.com/mhue-ai/timpi-cise/internal/server"
)

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
		cfgPath   = flag.String("config", defaultConfigPath(), "path to config file (created if missing)")
		addr      = flag.String("addr", "", "override dashboard listen address (e.g. 127.0.0.1:8770)")
		noOpen    = flag.Bool("no-open", false, "do not open the dashboard in a browser")
		autostart = flag.Bool("start", false, "begin polling immediately on launch")
		verbose   = flag.Bool("verbose", false, "log at debug level")
	)
	flag.Parse()

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
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	cfg.Sanitize()

	logger, closeLog := setupLogging(cfg, *verbose)
	defer closeLog()

	met := metrics.New(50)
	run := runner.New(cfg, *cfgPath, met, logger)
	defer run.Close()
	srv := server.New(run, met, logger)

	logger.Info("timpi-cise starting", "mode", cfg.Mode, "addr", cfg.Server.Addr, "log_dir", cfg.Logging.Dir)

	if *autostart {
		if err := run.Start(); err != nil {
			logger.Warn("autostart skipped", "err", err)
		}
	}

	url := "http://" + normalizeHost(srv.Addr())
	fmt.Println("timpi-cise dashboard:", url)
	fmt.Println("mode:", cfg.Mode, "| interval:", cfg.PollSeconds, "s (min 60) | config:", *cfgPath)
	fmt.Println("Press Ctrl+C to quit.")

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

	var fatal error
	select {
	case <-ctx.Done():
		fmt.Println("\nshutting down…")
	case err := <-serverErr:
		logger.Error("dashboard server failed", "addr", cfg.Server.Addr, "err", err)
		fatal = err
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
		if err := os.MkdirAll(cfg.Logging.Dir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "warning: cannot create log dir %q: %v (logging to terminal only)\n", cfg.Logging.Dir, err)
		} else if f, ferr := os.OpenFile(cfg.AppLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr != nil {
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
