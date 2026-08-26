// Package media derives and caches alternative renditions of archived videos.
//
// TubeArchivist stores one muxed file per video. Anything else a client needs —
// audio only today, an Apple-compatible rendition later — is derived from that
// file on first request and cached on disk. Every entry can be rebuilt from
// TubeArchivist, so losing the directory costs CPU and nothing else, which is
// what lets the cache live on ephemeral storage.
package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DeriveFunc writes a rendition to dst. It must either produce a complete file
// or return an error; the caller only publishes dst on success.
type DeriveFunc func(ctx context.Context, dst string) error

// deriveTimeout bounds a single derivation. A remux runs far faster than
// realtime, so this is a stuck-process guard rather than a real budget.
const deriveTimeout = 30 * time.Minute

type Cache struct {
	dir      string
	maxBytes int64
	log      *slog.Logger

	mu       sync.Mutex
	inflight map[string]*job
}

type job struct {
	done chan struct{}
	err  error
}

func NewCache(dir string, maxBytes int64, log *slog.Logger) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("media cache: dir is required")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("media cache: %w", err)
	}
	return &Cache{dir: dir, maxBytes: maxBytes, log: log, inflight: map[string]*job{}}, nil
}

// Get returns the path of the cached rendition, deriving it first if needed.
// Concurrent callers for the same name wait on a single derivation rather than
// each starting their own — without this, several listeners opening the same
// track would each spawn ffmpeg over the same source.
func (c *Cache) Get(ctx context.Context, name string, derive DeriveFunc) (string, error) {
	path := filepath.Join(c.dir, name)
	if c.touch(path) {
		return path, nil
	}

	c.mu.Lock()
	if j, ok := c.inflight[name]; ok {
		c.mu.Unlock()
		select {
		case <-j.done:
			if j.err != nil {
				return "", j.err
			}
			return path, nil
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	j := &job{done: make(chan struct{})}
	c.inflight[name] = j
	c.mu.Unlock()

	j.err = c.derive(ctx, path, derive)
	close(j.done)
	c.mu.Lock()
	delete(c.inflight, name)
	c.mu.Unlock()
	if j.err != nil {
		return "", j.err
	}
	return path, nil
}

func (c *Cache) derive(ctx context.Context, path string, derive DeriveFunc) error {
	// Detached from the caller: one client navigating away must not abandon a
	// derivation others are already waiting on.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), deriveTimeout)
	defer cancel()

	tmp, err := os.CreateTemp(c.dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("media cache: %w", err)
	}
	tmpName := tmp.Name()
	// ffmpeg writes the file itself; we only needed the reserved name.
	_ = tmp.Close()
	// Removed on every failure path so a killed derivation leaves no partial
	// file behind; after a successful rename this is a no-op.
	defer func() { _ = os.Remove(tmpName) }()

	if err := derive(ctx, tmpName); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("media cache: publish: %w", err)
	}
	c.evict()
	return nil
}

// touch marks an entry as recently used and reports whether it exists. Access
// time is the LRU signal, recorded as mtime because that is what survives on
// filesystems mounted with noatime.
func (c *Cache) touch(path string) bool {
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		return false
	}
	now := time.Now()
	_ = os.Chtimes(path, now, now)
	return true
}

// evict deletes least-recently-used entries until the cache is under its cap.
func (c *Cache) evict() {
	if c.maxBytes <= 0 {
		return
	}
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type item struct {
		path string
		size int64
		used time.Time
	}
	var (
		items []item
		total int64
	)
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) == ".tmp" || len(e.Name()) > 0 && e.Name()[0] == '.' {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		items = append(items, item{filepath.Join(c.dir, e.Name()), info.Size(), info.ModTime()})
		total += info.Size()
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })
	for _, it := range items {
		if total <= c.maxBytes {
			return
		}
		if err := os.Remove(it.path); err != nil {
			continue // raced with another eviction or a read; skip it
		}
		total -= it.size
		if c.log != nil {
			c.log.Info("media cache evicted", "file", filepath.Base(it.path), "bytes", it.size)
		}
	}
}
