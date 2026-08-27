package media

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// JobState is what a directory entry's derivation is doing. It is reported to
// clients verbatim (see `hls_state` in docs/api.md), so the values are part of
// the API.
type JobState string

const (
	// StatePending means nobody has asked for the entry yet, so nothing is
	// running and nothing is on disk.
	StatePending JobState = "pending"
	// StateRunning covers both "ffmpeg is working" and "queued behind another
	// job" — from a client's side they are the same wait.
	StateRunning JobState = "running"
	StateDone    JobState = "done"
	// StateFailed is remembered so a client can be told, but it does not stick:
	// the next request starts the job again.
	StateFailed JobState = "failed"
)

// ErrNotReady is returned by WaitDir when the deadline passes with the entry
// still not usable. The job keeps running; the caller should tell the client to
// come back.
var ErrNotReady = errors.New("media cache: entry not ready yet")

// ErrClosed is returned once the cache is shutting down.
var ErrClosed = errors.New("media cache: closed")

// doneMarker is written into a directory entry when its job completes. It is
// what makes "done" survive a restart: the in-memory job table does not, and
// a directory on disk is otherwise indistinguishable from one a killed process
// left half-written.
const doneMarker = ".complete"

type dirJob struct {
	done chan struct{}

	mu    sync.Mutex
	state JobState
	err   error
}

func (j *dirJob) State() JobState {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state
}

func (j *dirJob) Err() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

func (j *dirJob) finish(err error) {
	j.mu.Lock()
	j.err = err
	j.state = StateDone
	if err != nil {
		j.state = StateFailed
	}
	j.mu.Unlock()
	close(j.done)
}

// StartDir makes sure name is being derived and reports where that stands. It
// never blocks on the derivation: the first request kicks it off and returns,
// so a client can prefetch, and the request that actually needs the output
// waits with WaitDir.
//
// A finished entry is returned as done without touching ffmpeg. A previous
// failure is not sticky — it is retried here — which is what keeps one bad
// run from wedging a video forever.
func (c *Cache) StartDir(name string, derive DirDeriveFunc) JobState {
	return c.StartDirJob(name, nil, derive)
}

// StartDirJob is StartDir with a preparation step that runs *before* the job
// queues for a transcode slot. An HLS job uses it to write its playlist, so a
// client can seek in a rendition that is still waiting behind another
// transcode — the playlist is derived from the video's duration and needs no
// encoder to exist.
func (c *Cache) StartDirJob(name string, prepare, derive DirDeriveFunc) JobState {
	path := filepath.Join(c.dir, name)

	c.mu.Lock()
	defer c.mu.Unlock()
	if j, ok := c.dirs[name]; ok && j.State() == StateRunning {
		return StateRunning
	}
	if c.complete(path) {
		c.touchDir(path)
		delete(c.dirs, name)
		return StateDone
	}
	if c.closed {
		return StateFailed
	}
	j := &dirJob{done: make(chan struct{}), state: StateRunning}
	c.dirs[name] = j
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		err := c.deriveDir(path, prepare, derive)
		j.finish(err)
		if err != nil {
			if c.log != nil {
				c.log.Error("media derivation failed", "entry", name, "err", err)
			}
			// A failure is kept in the table so DirState can report it; the
			// next StartDir replaces it. A success is dropped: the marker on
			// disk says everything the table would, and the table would
			// otherwise grow by one entry per video forever.
			return
		}
		c.mu.Lock()
		if c.dirs[name] == j {
			delete(c.dirs, name)
		}
		c.mu.Unlock()
	}()
	return StateRunning
}

// deriveDir runs one directory derivation: prepare, wait for a slot, build in
// place, and mark it complete. On any failure the partial directory is removed,
// so the next request starts clean rather than serving a truncated rendition.
func (c *Cache) deriveDir(path string, prepare, derive DirDeriveFunc) error {
	// Detached from any request: a viewer navigating away must not abandon a
	// transcode others are waiting on, or leave the cache holding half of one.
	ctx, cancel := context.WithTimeout(c.baseCtx, transcodeTimeout)
	defer cancel()

	// The entry is deliberately *not* emptied first. A directory a killed
	// process left behind is work already paid for, and an HLS job picks up
	// from what is in it rather than encoding it all again. Every failure path
	// below removes the directory, so what survives to be resumed is only ever
	// the output of a run that did not fail.
	if err := os.MkdirAll(path, 0o750); err != nil {
		return fmt.Errorf("media cache: create entry: %w", err)
	}
	if prepare != nil {
		if err := prepare(ctx, path); err != nil {
			_ = os.RemoveAll(path)
			return err
		}
	}

	select {
	case c.slots <- struct{}{}:
	case <-c.baseCtx.Done():
		_ = os.RemoveAll(path)
		return ErrClosed
	}
	defer func() { <-c.slots }()

	if err := derive(ctx, path); err != nil {
		_ = os.RemoveAll(path)
		return err
	}
	if err := os.WriteFile(filepath.Join(path, doneMarker), nil, 0o600); err != nil {
		_ = os.RemoveAll(path)
		return fmt.Errorf("media cache: mark complete: %w", err)
	}
	c.touchDir(path)
	c.evict()
	return nil
}

// WaitDir blocks until ready reports the entry usable, the job ends, or the
// deadline passes. It is how the playlist request waits for the first segment
// without waiting for the whole transcode.
//
// The caller's context is honoured: a client that hangs up stops waiting, but
// the job behind it carries on.
func (c *Cache) WaitDir(ctx context.Context, name string, ready func(dir string) bool, timeout time.Duration) (string, error) {
	path := filepath.Join(c.dir, name)
	j := c.dirJob(name)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	poll := time.NewTicker(dirPollInterval)
	defer poll.Stop()

	for {
		if ready(path) {
			c.touchDir(path)
			return path, nil
		}
		if j == nil {
			return "", ErrNotReady
		}
		select {
		case <-j.done:
			if err := j.Err(); err != nil {
				return "", err
			}
			if ready(path) {
				c.touchDir(path)
				return path, nil
			}
			return "", fmt.Errorf("media cache: %s finished without usable output", name)
		case <-poll.C:
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", ErrNotReady
		}
	}
}

// dirPollInterval is how often WaitDir re-checks the entry. ffmpeg gives no
// signal when it publishes a segment, and a stat plus a short read is cheap
// next to the transcode itself.
const dirPollInterval = 150 * time.Millisecond

// DirState reports a directory entry's state for the API. On-disk completeness
// wins over the job table: an entry that finished in an earlier process is
// done, and one that was evicted since is pending again — in both cases what a
// client would get if it asked now.
func (c *Cache) DirState(name string) JobState {
	path := filepath.Join(c.dir, name)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.complete(path) {
		return StateDone
	}
	j, ok := c.dirs[name]
	if !ok {
		return StatePending
	}
	switch j.State() {
	case StateRunning:
		return StateRunning
	case StateFailed:
		return StateFailed
	default:
		return StatePending
	}
}

// TouchDir marks a directory entry as recently used, keeping a rendition that
// is being watched ahead of one that is not in the eviction order.
func (c *Cache) TouchDir(name string) { c.touchDir(filepath.Join(c.dir, name)) }

// Dir returns the on-disk path of a directory entry. It does not check that
// the entry exists.
func (c *Cache) Dir(name string) string { return filepath.Join(c.dir, name) }

// Close cancels every running job (killing its ffmpeg) and waits for them to
// clean up, so a shutting-down server leaves no orphaned process and no
// half-written entry behind.
func (c *Cache) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.wg.Wait()
}

func (c *Cache) dirJob(name string) *dirJob {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.dirs[name]
}

// runningDirs is the set of entries eviction must leave alone: the ones still
// being written. An entry that is already marked complete is fair game even
// while its goroutine is unwinding — otherwise the eviction pass at the end of
// a job would skip every entry that just finished.
func (c *Cache) runningDirs() map[string]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]bool, len(c.dirs))
	for name, j := range c.dirs {
		if j.State() == StateRunning && !c.complete(filepath.Join(c.dir, name)) {
			out[name] = true
		}
	}
	return out
}

// complete reports whether a directory entry finished. Callers may or may not
// hold c.mu; it only touches the filesystem.
func (c *Cache) complete(path string) bool {
	st, err := os.Stat(filepath.Join(path, doneMarker))
	return err == nil && !st.IsDir()
}

// dirSize sums the files in a directory entry, which is what it costs the
// cache. Errors mid-walk are skipped rather than failing the whole eviction
// pass: a file disappearing under it is exactly what eviction is doing.
func dirSize(path string) (int64, error) {
	var total int64
	err := filepath.WalkDir(path, func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // a vanished file is not a reason to abandon the walk
		}
		if info, err := d.Info(); err == nil {
			total += info.Size()
		}
		return nil
	})
	return total, err
}
