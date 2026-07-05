// Package rotate provides a size-based rotating file writer, used for the app
// log so it cannot grow without bound. When the file would exceed the size
// limit it is renamed with a timestamp and a fresh file is started.
package rotate

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Writer is an io.WriteCloser that rotates its backing file by size.
type Writer struct {
	mu       sync.Mutex
	path     string
	max      int64
	f        *os.File
	size     int64
	rotateAt int64
}

// New opens (appending to) path and rotates once it would exceed maxBytes.
func New(path string, maxBytes int64) (*Writer, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	var size int64
	if info, serr := f.Stat(); serr == nil {
		size = info.Size()
	}
	return &Writer{path: path, max: maxBytes, f: f, size: size, rotateAt: maxBytes}, nil
}

// Write implements io.Writer, rotating first if the write would exceed the size
// limit.
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.max > 0 && w.size+int64(len(p)) > w.rotateAt {
		w.rotate()
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// rotate renames the current file with a timestamp and opens a fresh one. If the
// rename fails, it keeps appending to the current file (never truncating, so no
// log data is lost) and defers the next attempt. The caller must hold w.mu.
func (w *Writer) rotate() {
	if err := w.f.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "log rotate: close failed: %v\n", err)
	}
	ts := time.Now().Format("20060102-150405")
	ext := filepath.Ext(w.path)
	base := w.path[:len(w.path)-len(ext)]

	if err := os.Rename(w.path, base+"-"+ts+ext); err != nil {
		// Do not truncate — reopen for append and back off the next rotation.
		fmt.Fprintf(os.Stderr, "log rotate: rename failed, continuing on current file: %v\n", err)
		f, oerr := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if oerr != nil {
			fmt.Fprintf(os.Stderr, "log rotate: reopen failed: %v\n", oerr)
			return
		}
		w.f = f
		if s, serr := f.Stat(); serr == nil {
			w.size = s.Size()
		}
		w.rotateAt = w.size + w.max
		return
	}

	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "log rotate: open fresh file failed: %v\n", err)
		return
	}
	w.f = f
	w.size = 0
	w.rotateAt = w.max
}

// Close closes the underlying file.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
