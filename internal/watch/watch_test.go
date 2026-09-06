package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func newWatcher(t *testing.T) *Watcher {
	t.Helper()
	w, err := New()
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func write(t *testing.T, path, s string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func expectSignal(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case <-w.Events():
	case <-time.After(2 * time.Second):
		t.Fatal("no signal")
	}
}

func expectQuiet(t *testing.T, w *Watcher) {
	t.Helper()
	select {
	case <-w.Events():
		t.Fatal("unexpected signal")
	case <-time.After(3 * debounce):
	}
}

func TestWriteSignals(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	write(t, f, "one\n")
	w := newWatcher(t)
	w.Set([]string{f})
	write(t, f, "two\n")
	expectSignal(t, w)
}

func TestBurstIsOneSignal(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	write(t, f, "one\n")
	w := newWatcher(t)
	w.Set([]string{f})
	write(t, f, "two\n")
	write(t, f, "three\n")
	expectSignal(t, w)
	expectQuiet(t, w)
}

func TestUnsetIsQuiet(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	write(t, f, "one\n")
	w := newWatcher(t)
	w.Set([]string{f})
	w.Set(nil)
	write(t, f, "two\n")
	expectQuiet(t, w)
}

func TestSetIsCapped(t *testing.T) {
	dir := t.TempDir()
	var paths []string
	for i := range maxWatched + 10 {
		p := filepath.Join(dir, strconv.Itoa(i))
		write(t, p, "")
		paths = append(paths, p)
	}
	w := newWatcher(t)
	w.Set(paths)
	if got := len(w.Paths()); got != maxWatched {
		t.Fatalf("watching %d paths, want %d", got, maxWatched)
	}
}
