// Package watcher provides file watching with debouncing for Go source files.
package watcher

import (
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// DefaultDebounce is the default debounce duration
const DefaultDebounce = 100 * time.Millisecond

// Options configures the watcher
type Options struct {
	// Paths to watch (directories)
	Paths []string

	// Extensions to watch (e.g., ".go", ".mod")
	Extensions []string

	// IgnorePaths are path prefixes to ignore
	IgnorePaths []string

	// Debounce duration for batching rapid changes
	Debounce time.Duration

	// Logger for watcher events
	Logger *log.Logger
}

func (o *Options) defaults() {
	if len(o.Paths) == 0 {
		o.Paths = []string{"."}
	}
	if len(o.Extensions) == 0 {
		o.Extensions = []string{".go", ".mod", ".sum"}
	}
	if o.Debounce == 0 {
		o.Debounce = DefaultDebounce
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
}

// Watcher watches for file changes
type Watcher struct {
	opts     Options
	fsw      *fsnotify.Watcher
	onChange func()
	done     chan struct{}
	mu       sync.Mutex
	timer    *time.Timer
}

// New creates a new file watcher
func New(opts Options) (*Watcher, error) {
	opts.defaults()

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	return &Watcher{
		opts: opts,
		fsw:  fsw,
		done: make(chan struct{}),
	}, nil
}

// SetOnChange sets the callback for file changes
func (w *Watcher) SetOnChange(fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.onChange = fn
}

// Start begins watching for file changes
func (w *Watcher) Start() error {
	// Add all paths to watch
	for _, path := range w.opts.Paths {
		if err := w.addRecursive(path); err != nil {
			return err
		}
	}

	go w.eventLoop()
	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.done)
	w.fsw.Close()
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Skip errors
		}

		// Check if path should be ignored
		if w.shouldIgnore(path) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Only watch directories
		if info.IsDir() {
			if err := w.fsw.Add(path); err != nil {
				w.opts.Logger.Printf("[watcher] failed to watch %s: %v", path, err)
			}
		}

		return nil
	})
}

func (w *Watcher) shouldIgnore(path string) bool {
	// Always ignore common non-source directories
	base := filepath.Base(path)
	if base == ".git" || base == "vendor" || base == "node_modules" || base == "tmp" {
		return true
	}

	// Check configured ignore paths
	for _, ignore := range w.opts.IgnorePaths {
		if strings.HasPrefix(path, ignore) {
			return true
		}
	}

	return false
}

func (w *Watcher) shouldProcess(path string) bool {
	if w.shouldIgnore(path) {
		return false
	}

	ext := filepath.Ext(path)
	for _, e := range w.opts.Extensions {
		if ext == e {
			return true
		}
	}

	return false
}

func (w *Watcher) eventLoop() {
	for {
		select {
		case <-w.done:
			return

		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}

			// Only process relevant file types
			if !w.shouldProcess(event.Name) {
				continue
			}

			// Skip chmod events
			if event.Op == fsnotify.Chmod {
				continue
			}

			w.opts.Logger.Printf("[watcher] %s: %s", event.Op, event.Name)

			// Handle new directories
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					w.addRecursive(event.Name)
				}
			}

			// Debounce the change notification
			w.debounce()

		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.opts.Logger.Printf("[watcher] error: %v", err)
		}
	}
}

func (w *Watcher) debounce() {
	w.mu.Lock()
	defer w.mu.Unlock()

	// Reset the timer if it exists
	if w.timer != nil {
		w.timer.Stop()
	}

	// Create a new timer
	w.timer = time.AfterFunc(w.opts.Debounce, func() {
		w.mu.Lock()
		onChange := w.onChange
		w.mu.Unlock()

		if onChange != nil {
			onChange()
		}
	})
}
