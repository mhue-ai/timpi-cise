package rotate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotateBySize(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	w, err := New(path, 100) // rotate at 100 bytes
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := w.Write([]byte("some log line that is a few dozen bytes long\n")); err != nil {
			t.Fatal(err)
		}
	}
	w.Close()

	entries, _ := os.ReadDir(dir)
	var archives int
	for _, e := range entries {
		if e.Name() != "app.log" && strings.HasPrefix(e.Name(), "app-") {
			archives++
		}
	}
	if archives == 0 {
		t.Fatalf("expected rotated archives, found %v", entries)
	}
	// The current file must exist and be under the cap+one-line.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() > 200 {
		t.Errorf("current log unexpectedly large: %d", info.Size())
	}
}

func TestNoRotateUnderCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.log")
	w, _ := New(path, 1<<20)
	_, _ = w.Write([]byte("small\n"))
	w.Close()
	// Only the one file should exist.
	dir := filepath.Dir(path)
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("expected 1 file, got %d", len(entries))
	}
}
