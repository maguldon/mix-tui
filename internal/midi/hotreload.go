package midi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// fileStamp is the cheap change signal for a mapping file. mtime+size catches
// an edit without reading every file's contents on each poll.
type fileStamp struct {
	mod  time.Time
	size int64
}

// Watcher polls a directory of mapping files and swaps the handler's table when
// a file changes. Polling rather than fsnotify: the config directory is tiny and
// a 2s interval is imperceptible, and it avoids a dependency for something this
// simple (plan constraint: no new deps beyond gomidi and yaml.v3).
type Watcher struct {
	dir  string
	h    *Handler
	logf func(string, ...any)

	mu   sync.Mutex
	seen map[string]fileStamp
}

// NewWatcher returns a Watcher over dir, installing tables into h. logf may be
// nil.
func NewWatcher(dir string, h *Handler, logf func(string, ...any)) *Watcher {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Watcher{dir: dir, h: h, logf: logf, seen: make(map[string]fileStamp)}
}

// ReloadOnce performs one scan, parsing and compiling every mapping whose
// mtime or size changed. It returns how many files were installed and any
// per-file errors.
//
// A file is installed only when it both parses and compiles, and its stamp is
// recorded only on success. That is deliberate: a file left broken keeps being
// retried and re-reported on every scan, so fixing it takes effect immediately
// instead of waiting for the next unrelated edit. Errors are collected and
// scanning continues — one bad file must not block the rest.
func (w *Watcher) ReloadOnce() (changed int, errs []error) {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return 0, []error{err}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(w.dir, name)
		info, err := e.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		stamp := fileStamp{mod: info.ModTime(), size: info.Size()}

		w.mu.Lock()
		prev, seen := w.seen[path]
		w.mu.Unlock()
		if seen && prev == stamp {
			continue // unchanged
		}

		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m, err := ParseMapping(path, data)
		if err != nil {
			errs = append(errs, err)
			w.logf("mixplay: keeping previous mapping: %v", err)
			continue
		}
		tab, err := m.Compile()
		if err != nil {
			errs = append(errs, err)
			w.logf("mixplay: keeping previous mapping: %v", err)
			continue
		}
		w.h.SetTable(tab)
		w.mu.Lock()
		w.seen[path] = stamp
		w.mu.Unlock()
		changed++
	}
	return changed, errs
}

// Run polls until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, errs := w.ReloadOnce()
			if n > 0 {
				w.logf("mixplay: reloaded %d mapping(s)", n)
			}
			for _, e := range errs {
				w.logf("mixplay: mapping error: %v", e)
			}
		}
	}
}

// LoadDeviceTables parses every mapping in dir into a name->Table map. Files
// that fail to parse or compile are reported in errs and omitted, so one broken
// mapping does not prevent the others loading.
func LoadDeviceTables(dir string) (map[string]*Table, []error) {
	tables := make(map[string]*Table)
	var errs []error
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, []error{err}
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := strings.ToLower(filepath.Ext(e.Name()))
		if ext != ".yaml" && ext != ".yml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		m, err := ParseMapping(path, data)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		tab, err := m.Compile()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		tables[tab.Device] = tab
	}
	return tables, errs
}
