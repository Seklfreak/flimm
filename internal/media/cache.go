// Package media derives and caches alternative renditions of archived videos.
//
// TubeArchivist stores one muxed file per video. Anything else a client needs —
// two audio renditions (Opus in WebM for browsers, AAC in MP4 for
// AVFoundation) and a compatible H.264 video rendition as HLS — is derived
// from that file on first request and cached on disk. Every entry can be
// rebuilt from TubeArchivist, so losing the directory costs CPU and nothing
// else, which is what lets the cache live on ephemeral storage.
//
// An entry is either a single file (the audio variants) or a directory (HLS:
// a playlist, an init segment and the media segments). Files are derived to a
// temp name and published by rename, so a reader never sees a partial one.
// Directories cannot work that way — the point of HLS here is that a viewer
// starts on the first segment while the rest is still being written — so they
// are built in place, tracked as jobs, and removed wholesale on failure.
package media

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// DeriveFunc writes a rendition to dst. It must either produce a complete file
// or return an error; the caller only publishes dst on success.
type DeriveFunc func(ctx context.Context, dst string) error

// DirDeriveFunc writes a multi-file rendition into dir, which already exists
// but is not necessarily empty: a job left half-finished by a killed process
// resumes from what is in it. Unlike a DeriveFunc it is read while it runs, so
// what it publishes has to be readable the moment it appears (the playlist is
// written whole and by rename; each segment by rename).
type DirDeriveFunc func(ctx context.Context, dir string) error

const (
	// deriveTimeout bounds a single file derivation. A remux runs far faster
	// than realtime, so this is a stuck-process guard rather than a budget.
	deriveTimeout = 30 * time.Minute
	// transcodeTimeout bounds a directory job. A video transcode can run near
	// realtime on a busy box, so a long video needs hours, not minutes.
	transcodeTimeout = 4 * time.Hour
)

type Cache struct {
	dir      string
	maxBytes int64
	log      *slog.Logger

	mu       sync.Mutex
	inflight map[string]*job
	dirs     map[string]*dirJob
	closed   bool

	// slots caps concurrent directory jobs. A transcode is CPU-bound, so
	// running four at once does not finish any of them sooner — it only makes
	// the first viewer wait four times as long for the first segment.
	slots chan struct{}
	// baseCtx outlives every request: a job runs to completion whoever walks
	// away from it, and is cancelled only by Close.
	baseCtx context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

type job struct {
	done chan struct{}
	err  error
}

// NewCache opens (creating it if needed) the cache directory. maxJobs caps
// concurrent directory derivations; 0 or less means one at a time.
func NewCache(dir string, maxBytes int64, maxJobs int, log *slog.Logger) (*Cache, error) {
	if dir == "" {
		return nil, errors.New("media cache: dir is required")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("media cache: %w", err)
	}
	if maxJobs < 1 {
		maxJobs = 1
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Cache{
		dir:      dir,
		maxBytes: maxBytes,
		log:      log,
		inflight: map[string]*job{},
		dirs:     map[string]*dirJob{},
		slots:    make(chan struct{}, maxJobs),
		baseCtx:  ctx,
		cancel:   cancel,
	}, nil
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

// touchDir is touch for a directory entry: the directory's own mtime is the
// LRU signal, refreshed whenever any file inside it is served.
func (c *Cache) touchDir(path string) {
	now := time.Now()
	_ = os.Chtimes(path, now, now)
}

// evict deletes least-recently-used entries until the cache is under its cap.
// A directory counts as one entry sized by the sum of its files and is removed
// whole — half a rendition is worse than none. A directory whose job is still
// running is never evicted: it is about to grow, and a player is reading it.
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
	running := c.runningDirs()
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".tmp" || strings.HasPrefix(name, ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		size, used := info.Size(), info.ModTime()
		if e.IsDir() {
			if size, err = dirSize(filepath.Join(c.dir, name)); err != nil {
				continue
			}
			if running[name] {
				// Counted against the cap — it is really on the disk — but
				// never a candidate: a player is reading it and the job is
				// still writing it.
				total += size
				continue
			}
		}
		items = append(items, item{filepath.Join(c.dir, name), size, used})
		total += size
	}
	if total <= c.maxBytes {
		return
	}
	sort.Slice(items, func(i, j int) bool { return items[i].used.Before(items[j].used) })
	for _, it := range items {
		if total <= c.maxBytes {
			return
		}
		if err := os.RemoveAll(it.path); err != nil {
			continue // raced with another eviction or a read; skip it
		}
		total -= it.size
		if c.log != nil {
			c.log.Info("media cache evicted", "entry", filepath.Base(it.path), "bytes", it.size)
		}
	}
}
