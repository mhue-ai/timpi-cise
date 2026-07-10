package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSaveCreatesParentDir guards the fix for the "config never persists → the
// next start falls back to dry-run" bug: Save must create missing parent dirs.
func TestSaveCreatesParentDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "nested", "config.json")
	c := Default()
	c.Mode = ModeBrowser
	if err := Save(path, c); err != nil {
		t.Fatalf("Save into a non-existent dir should succeed, got: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Mode != ModeBrowser {
		t.Errorf("persisted mode = %q, want browser", got.Mode)
	}
}
