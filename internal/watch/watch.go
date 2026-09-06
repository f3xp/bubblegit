// Package watch turns file-system events on a bounded set of paths into one
// debounced signal. It reads nothing itself: the UI treats the signal as a
// request to re-read the working tree, the way lazygit's file watcher does.
package watch

import (
	"time"

	"github.com/fsnotify/fsnotify"
)

// maxWatched caps how many paths are watched at once — lazygit's
// MAX_WATCHED_FILES. On macOS every watched file holds a kqueue descriptor, and
// a repository with thousands of modified files would otherwise exhaust them.
// Anything past the cap is picked up by the periodic refresh instead.
const maxWatched = 50

// debounce coalesces the burst of events one save produces — a chmod and two
// writes from most editors, a remove and a create from those that save by
// rename — into a single signal.
const debounce = 100 * time.Millisecond

// Watcher watches a replaceable set of files and reports, without saying
// which, that one of them changed.
type Watcher struct {
	fs    *fsnotify.Watcher
	paths []string
	ch    chan struct{}
}

// New starts a watcher with nothing under watch yet.
func New() (*Watcher, error) {
	fs, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{fs: fs, ch: make(chan struct{}, 1)}
	go w.loop()
	return w, nil
}

// Set replaces the watched set. Paths past maxWatched are dropped. Errors from
// adding or removing are ignored: a path may have been deleted since the
// status that named it, and a watch lost to a rename is re-added by the next
// Set, which every status re-read issues.
func (w *Watcher) Set(paths []string) {
	for _, p := range w.paths {
		_ = w.fs.Remove(p)
	}
	if len(paths) > maxWatched {
		paths = paths[:maxWatched]
	}
	w.paths = w.paths[:0]
	for _, p := range paths {
		if err := w.fs.Add(p); err == nil {
			w.paths = append(w.paths, p)
		}
	}
}

// Paths reports what is currently under watch.
func (w *Watcher) Paths() []string { return w.paths }

// Events delivers one signal per debounced burst of changes. The channel
// holds one signal; a burst that arrives while one is already waiting is
// folded into it, since the reader re-reads everything either way.
func (w *Watcher) Events() <-chan struct{} { return w.ch }

func (w *Watcher) loop() {
	var timer *time.Timer
	var fire <-chan time.Time
	for {
		select {
		case _, ok := <-w.fs.Events:
			if !ok {
				return
			}
			if timer == nil {
				timer = time.NewTimer(debounce)
			} else {
				timer.Reset(debounce)
			}
			fire = timer.C
		case _, ok := <-w.fs.Errors:
			// Informational — an overflow or a watch the kernel dropped. The
			// next Set re-adds what matters.
			if !ok {
				return
			}
		case <-fire:
			fire = nil
			select {
			case w.ch <- struct{}{}:
			default:
			}
		}
	}
}
