package runner

import (
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/mhue-ai/timpi-cise/internal/config"
	"github.com/mhue-ai/timpi-cise/internal/metrics"
)

// TestConcurrentControl hammers the runner's control surface from many
// goroutines to shake out deadlocks, panics, and (under -race) data races on the
// shared config/state. Dry-run mode keeps it fast and network-free.
func TestConcurrentControl(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	met := metrics.New(10)
	cfg := config.Default()
	cfg.Mode = config.ModeDryRun       // no network/browser during the stress test
	cfg.Generation.LLM.Enabled = false // don't call a model server
	// Disable the results CSV so the test touches no disk.
	cfg.Logging.CSVResults = false
	cfg.Logging.AppLog = false
	r := New(cfg, "", met, logger)
	defer r.Close()

	var wg sync.WaitGroup
	stop := make(chan struct{})

	worker := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					fn()
				}
			}
		}()
	}

	worker(func() { _ = r.Start() })
	worker(func() { r.Stop() })
	worker(func() {
		c := r.Config()
		c.PollSeconds = 60 + int(time.Now().UnixNano()%120)
		c.JitterSeconds = int(time.Now().UnixNano() % 30)
		_ = r.UpdateConfig(c)
	})
	worker(func() { _ = r.Config() })
	worker(func() { _ = r.AdapterName() })
	worker(func() { _ = r.Running() })
	worker(func() { _, _ = r.CSVInfo() })
	worker(func() { _ = met.Snapshot() })

	time.Sleep(300 * time.Millisecond)
	close(stop)
	wg.Wait()
}

// TestSafetyFloorNotBypassable verifies the 60s minimum poll interval survives
// attempts to set it lower via config, and that nextWait never returns less than
// the floor even on the happy path with zero jitter.
func TestSafetyFloorNotBypassable(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	met := metrics.New(10)
	cfg := config.Default()
	cfg.Mode = config.ModeDryRun
	cfg.Generation.LLM.Enabled = false
	cfg.PollSeconds = 1 // below the floor
	cfg.JitterSeconds = 0
	cfg.Logging.CSVResults = false
	cfg.Logging.AppLog = false
	r := New(cfg, "", met, logger)
	defer r.Close()

	if got := r.Config().PollSeconds; got < config.MinPollSeconds {
		t.Fatalf("config poll interval %d is below floor %d", got, config.MinPollSeconds)
	}

	min := time.Duration(config.MinPollSeconds) * time.Second
	if w := r.nextWait(r.Config(), true, 0); w < min {
		t.Fatalf("nextWait happy-path %v is below floor %v", w, min)
	}
	// A malicious short Retry-After must not shorten the wait below the floor.
	if w := r.nextWait(r.Config(), false, 1*time.Second); w < min {
		t.Fatalf("nextWait with tiny Retry-After %v is below floor %v", w, min)
	}
}
