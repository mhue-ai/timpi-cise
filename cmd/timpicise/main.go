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
	"log"
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
	var (
		cfgPath  = flag.String("config", defaultConfigPath(), "path to config file (created if missing)")
		addr     = flag.String("addr", "", "override dashboard listen address (e.g. 127.0.0.1:8770)")
		noOpen   = flag.Bool("no-open", false, "do not open the dashboard in a browser")
		autostart = flag.Bool("start", false, "begin polling immediately on launch")
	)
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if *addr != "" {
		cfg.Server.Addr = *addr
	}
	cfg.Sanitize()

	met := metrics.New(50)
	run := runner.New(cfg, *cfgPath, met)
	srv := server.New(run, met)

	if *autostart {
		if err := run.Start(); err != nil {
			log.Printf("autostart skipped: %v", err)
		}
	}

	url := "http://" + normalizeHost(srv.Addr())
	fmt.Println("timpi-cise dashboard:", url)
	fmt.Println("mode:", cfg.Mode, "| interval:", cfg.PollSeconds, "s (min 60) | config:", *cfgPath)
	fmt.Println("Press Ctrl+C to quit.")

	if !*noOpen {
		go openBrowserWhenReady(srv.Addr(), url)
	}

	// Graceful shutdown on signal.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		if err := srv.Start(); err != nil {
			log.Fatalf("server: %v", err)
		}
	}()

	<-ctx.Done()
	fmt.Println("\nshutting down…")
	run.Stop()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
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
	_ = exec.Command(cmd, args...).Start()
}

// ensure config dir exists is handled by config.Save via os.WriteFile failing
// gracefully; create the directory here for the default path.
func init() {
	if dir, err := os.UserConfigDir(); err == nil {
		_ = os.MkdirAll(filepath.Join(dir, "timpi-cise"), 0o755)
	}
}
