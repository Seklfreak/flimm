package media

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// writeIn puts one file of n bytes inside a directory entry.
func writeIn(t *testing.T, dir, name string, n int) error {
	t.Helper()
	return os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o600)
}

func newDirCache(t *testing.T, maxBytes int64, maxJobs int) *Cache {
	t.Helper()
	c, err := NewCache(t.TempDir(), maxBytes, maxJobs, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// waitState polls until the entry reaches want, so tests never depend on how
// fast a goroutine gets scheduled.
func waitState(t *testing.T, c *Cache, name string, want JobState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := c.DirState(name); got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("entry %q is %q, want %q", name, c.DirState(name), want)
}

// The states are reported to clients verbatim, so the whole life of a job has
// to be visible: never asked for, working, finished.
func TestDirStateThroughAJobsLife(t *testing.T) {
	c := newDirCache(t, 0, 1)
	release := make(chan struct{})
	derive := func(_ context.Context, dir string) error {
		<-release
		return writeIn(t, dir, "index.m3u8", 10)
	}

	if got := c.DirState("hls-a"); got != StatePending {
		t.Errorf("state before any request = %q, want %q", got, StatePending)
	}
	if got := c.StartDir("hls-a", derive); got != StateRunning {
		t.Errorf("StartDir = %q, want %q", got, StateRunning)
	}
	if got := c.DirState("hls-a"); got != StateRunning {
		t.Errorf("state while working = %q, want %q", got, StateRunning)
	}
	close(release)
	waitState(t, c, "hls-a", StateDone)

	// A finished entry is not derived again.
	var extra atomic.Int32
	if got := c.StartDir("hls-a", func(context.Context, string) error {
		extra.Add(1)
		return nil
	}); got != StateDone {
		t.Errorf("StartDir on a finished entry = %q, want %q", got, StateDone)
	}
	time.Sleep(50 * time.Millisecond)
	if n := extra.Load(); n != 0 {
		t.Errorf("a finished entry was derived %d more times", n)
	}
}

// Several viewers opening the same video must share one transcode.
func TestStartDirCollapsesConcurrentRequests(t *testing.T) {
	c := newDirCache(t, 0, 4)
	var calls atomic.Int32
	release := make(chan struct{})
	derive := func(_ context.Context, dir string) error {
		calls.Add(1)
		<-release
		return writeIn(t, dir, "index.m3u8", 10)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c.StartDir("hls-a", derive)
		}()
	}
	wg.Wait()
	close(release)
	waitState(t, c, "hls-a", StateDone)
	if n := calls.Load(); n != 1 {
		t.Errorf("started %d transcodes for one video, want 1", n)
	}
}

// A failure must not leave half a rendition on disk — a player would load the
// playlist and stall — and must not wedge the video either.
func TestFailedJobCleansUpAndRetries(t *testing.T) {
	c := newDirCache(t, 0, 1)
	boom := errors.New("ffmpeg exploded")
	first := c.StartDir("hls-a", func(_ context.Context, dir string) error {
		_ = writeIn(t, dir, "index.m3u8", 5) // partial output, as a killed ffmpeg leaves
		return boom
	})
	if first != StateRunning {
		t.Fatalf("StartDir = %q", first)
	}
	waitState(t, c, "hls-a", StateFailed)

	if _, err := os.Stat(c.Dir("hls-a")); !os.IsNotExist(err) {
		t.Error("a failed job left its partial directory behind")
	}
	// Not sticky: the next request tries again.
	if got := c.StartDir("hls-a", func(_ context.Context, dir string) error {
		return writeIn(t, dir, "index.m3u8", 10)
	}); got != StateRunning {
		t.Errorf("retry after a failure = %q, want %q", got, StateRunning)
	}
	waitState(t, c, "hls-a", StateDone)
}

// A transcode is CPU-bound: running several at once makes every viewer wait
// longer, so extra requests queue instead.
func TestConcurrentJobsAreCapped(t *testing.T) {
	c := newDirCache(t, 0, 1)
	var running, peak atomic.Int32
	release := make(chan struct{})
	derive := func(_ context.Context, dir string) error {
		n := running.Add(1)
		for {
			old := peak.Load()
			if n <= old || peak.CompareAndSwap(old, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return writeIn(t, dir, "index.m3u8", 10)
	}
	for _, name := range []string{"hls-a", "hls-b", "hls-c"} {
		c.StartDir(name, derive)
	}
	// Both queued jobs report as running: from a client's side, queued and
	// working are the same wait.
	if got := c.DirState("hls-c"); got != StateRunning {
		t.Errorf("queued entry = %q, want %q", got, StateRunning)
	}
	time.Sleep(100 * time.Millisecond)
	close(release)
	for _, name := range []string{"hls-a", "hls-b", "hls-c"} {
		waitState(t, c, name, StateDone)
	}
	if got := peak.Load(); got != 1 {
		t.Errorf("%d transcodes ran at once, want 1", got)
	}
}

// The scan lane is why a scrub preview appears at all on a box that transcodes:
// sharing the transcode lane put a minute of decoding behind an encode that
// runs for tens of minutes, and every client gives up long before that.
func TestScansDoNotQueueBehindATranscode(t *testing.T) {
	c := newDirCache(t, 0, 1)
	holding, release := make(chan struct{}), make(chan struct{})
	c.StartDir("hls-a", func(_ context.Context, dir string) error {
		close(holding)
		<-release
		return writeIn(t, dir, "index.m3u8", 10)
	})
	defer close(release)
	// Only once the transcode is inside its slot is there anything to queue
	// behind.
	<-holding

	c.StartScan("preview-a", func(_ context.Context, dir string) error {
		return writeIn(t, dir, PreviewTrackName, 10)
	})
	waitState(t, c, "preview-a", StateDone)
}

// The point of the whole design: the playlist request returns as soon as the
// first segments exist, not when the transcode ends.
func TestWaitDirReturnsBeforeTheJobFinishes(t *testing.T) {
	c := newDirCache(t, 0, 1)
	finish := make(chan struct{})
	c.StartDir("hls-a", func(_ context.Context, dir string) error {
		if err := writeIn(t, dir, "index.m3u8", 10); err != nil {
			return err
		}
		<-finish
		return writeIn(t, dir, "seg00000.m4s", 10)
	})
	ready := func(dir string) bool {
		_, err := os.Stat(filepath.Join(dir, "index.m3u8"))
		return err == nil
	}
	got, err := c.WaitDir(t.Context(), "hls-a", ready, 5*time.Second)
	if err != nil {
		t.Fatalf("WaitDir: %v", err)
	}
	if got != c.Dir("hls-a") {
		t.Errorf("WaitDir returned %q, want %q", got, c.Dir("hls-a"))
	}
	if state := c.DirState("hls-a"); state != StateRunning {
		t.Errorf("job should still be running, got %q", state)
	}
	close(finish)
	waitState(t, c, "hls-a", StateDone)
}

// A rendition that is slower than the wait is not an error: the client is told
// to come back, and the job keeps going.
func TestWaitDirTimesOutWithoutStoppingTheJob(t *testing.T) {
	c := newDirCache(t, 0, 1)
	finish := make(chan struct{})
	c.StartDir("hls-a", func(_ context.Context, dir string) error {
		<-finish
		return writeIn(t, dir, "index.m3u8", 10)
	})
	_, err := c.WaitDir(t.Context(), "hls-a", func(string) bool { return false }, 50*time.Millisecond)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("WaitDir err = %v, want %v", err, ErrNotReady)
	}
	if state := c.DirState("hls-a"); state != StateRunning {
		t.Errorf("the timed-out wait stopped the job: state %q", state)
	}
	close(finish)
	waitState(t, c, "hls-a", StateDone)
}

// A client hanging up ends its own wait and nothing else.
func TestWaitDirHonoursTheRequestContext(t *testing.T) {
	c := newDirCache(t, 0, 1)
	finish := make(chan struct{})
	defer close(finish)
	c.StartDir("hls-a", func(_ context.Context, dir string) error {
		<-finish
		return writeIn(t, dir, "index.m3u8", 10)
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := c.WaitDir(ctx, "hls-a", func(string) bool { return false }, time.Minute); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitDir err = %v, want context.Canceled", err)
	}
	if state := c.DirState("hls-a"); state != StateRunning {
		t.Errorf("a client hanging up stopped the job: state %q", state)
	}
}

// A failed job's error reaches whoever is waiting, rather than leaving them on
// the timeout.
func TestWaitDirReportsAFailure(t *testing.T) {
	c := newDirCache(t, 0, 1)
	boom := errors.New("ffmpeg exploded")
	c.StartDir("hls-a", func(context.Context, string) error { return boom })
	if _, err := c.WaitDir(t.Context(), "hls-a", func(string) bool { return false }, 5*time.Second); !errors.Is(err, boom) {
		t.Fatalf("WaitDir err = %v, want %v", err, boom)
	}
}

// A directory entry costs the sum of its files and is evicted whole: half a
// rendition is worse than none.
func TestEvictionAccountsForDirectoriesAndRemovesThemWhole(t *testing.T) {
	c := newDirCache(t, 250, 1)
	mk := func(_ context.Context, dir string) error {
		for _, name := range []string{"index.m3u8", "seg00000.m4s"} {
			if err := writeIn(t, dir, name, 50); err != nil {
				return err
			}
		}
		return nil
	}
	for _, name := range []string{"hls-a", "hls-b", "hls-c"} {
		c.StartDir(name, mk)
		waitState(t, c, name, StateDone)
		// Distinct timestamps so "least recently used" is well defined.
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(c.Dir("hls-a")); !os.IsNotExist(err) {
		t.Error("oldest rendition survived eviction")
	}
	for _, name := range []string{"hls-b", "hls-c"} {
		if _, err := os.Stat(filepath.Join(c.Dir(name), "seg00000.m4s")); err != nil {
			t.Errorf("recent rendition %q was evicted", name)
		}
	}
}

// Serving a segment counts as using the rendition, or a long video could be
// evicted out from under the player streaming it.
func TestTouchDirProtectsFromEviction(t *testing.T) {
	c := newDirCache(t, 250, 1)
	mk := func(_ context.Context, dir string) error { return writeIn(t, dir, "index.m3u8", 100) }
	for _, name := range []string{"hls-a", "hls-b"} {
		c.StartDir(name, mk)
		waitState(t, c, name, StateDone)
		time.Sleep(10 * time.Millisecond)
	}
	c.TouchDir("hls-a")
	time.Sleep(10 * time.Millisecond)
	c.StartDir("hls-c", mk)
	waitState(t, c, "hls-c", StateDone)

	if _, err := os.Stat(c.Dir("hls-a")); err != nil {
		t.Error("the rendition being watched was evicted")
	}
	if _, err := os.Stat(c.Dir("hls-b")); !os.IsNotExist(err) {
		t.Error("least recently used rendition survived")
	}
}

// Evicting a rendition that is still being written would delete the segments a
// viewer is waiting for and orphan the transcode producing them.
func TestEvictionSkipsRunningJobs(t *testing.T) {
	c := newDirCache(t, 150, 1)
	started := make(chan struct{})
	finish := make(chan struct{})
	c.StartDir("hls-slow", func(_ context.Context, dir string) error {
		if err := writeIn(t, dir, "index.m3u8", 200); err != nil {
			return err
		}
		close(started)
		<-finish
		return nil
	})
	<-started
	// A second, unrelated derivation drives an eviction pass over the cache
	// while the first job is still writing.
	if _, err := c.Get(t.Context(), "audio-b.webm", func(_ context.Context, dst string) error {
		return write(t, dst, 100)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Dir("hls-slow"), "index.m3u8")); err != nil {
		t.Errorf("a running job's output was evicted: %v", err)
	}
	// The unrelated entry is what the pass had to fall back to.
	if _, err := os.Stat(filepath.Join(c.Dir("audio-b.webm"))); !os.IsNotExist(err) {
		t.Error("eviction skipped the entry it could have taken")
	}
	// Once the job finishes the entry is an ordinary candidate again — it is
	// over the cap on its own here, so it goes. Only wait for the job to stop.
	close(finish)
	waitNotRunning(t, c, "hls-slow")
}

// waitNotRunning polls until the job ends, whatever it ends as.
func waitNotRunning(t *testing.T, c *Cache, name string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if c.DirState(name) != StateRunning {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("entry %q is still running", name)
}

// The job table dies with the process; the rendition on disk does not, so a
// restart must not re-transcode everything it had already finished.
func TestDoneSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.StartDir("hls-a", func(_ context.Context, d string) error { return writeIn(t, d, "index.m3u8", 10) })
	waitState(t, c, "hls-a", StateDone)
	c.Close()

	restarted, err := NewCache(dir, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted.Close)
	if got := restarted.DirState("hls-a"); got != StateDone {
		t.Errorf("state after a restart = %q, want %q", got, StateDone)
	}
}

// A directory a killed process left behind is not a rendition: it has no
// completion marker, so it reads as never derived and is rebuilt.
func TestUnmarkedDirectoryIsNotDone(t *testing.T) {
	c := newDirCache(t, 0, 1)
	leftover := c.Dir("hls-a")
	if err := os.MkdirAll(leftover, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := writeIn(t, leftover, "index.m3u8", 10); err != nil {
		t.Fatal(err)
	}
	if got := c.DirState("hls-a"); got != StatePending {
		t.Errorf("a half-written directory reads as %q, want %q", got, StatePending)
	}
	var called atomic.Int32
	c.StartDir("hls-a", func(_ context.Context, dir string) error {
		called.Add(1)
		return writeIn(t, dir, "index.m3u8", 20)
	})
	waitState(t, c, "hls-a", StateDone)
	if called.Load() != 1 {
		t.Error("a half-written directory was not rebuilt")
	}
}

// Shutting down must not leave an ffmpeg running or a partial entry on disk.
func TestCloseCancelsRunningJobs(t *testing.T) {
	c, err := NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	started, cancelled := make(chan struct{}), make(chan struct{})
	c.StartDir("hls-a", func(ctx context.Context, dir string) error {
		if err := writeIn(t, dir, "index.m3u8", 10); err != nil {
			return err
		}
		close(started)
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	})
	// Wait until ffmpeg would really be running: Close before that would only
	// prove the queue drains, not that the process is killed.
	<-started

	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the running job")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not wait for the job to unwind")
	}
	if _, err := os.Stat(c.Dir("hls-a")); !os.IsNotExist(err) {
		t.Error("a cancelled job left its partial directory behind")
	}
	// Nothing new starts after a shutdown.
	if got := c.StartDir("hls-b", func(context.Context, string) error { return nil }); got != StateFailed {
		t.Errorf("StartDir after Close = %q, want %q", got, StateFailed)
	}
}

// Closing the cache abandons whatever it was deriving. That is the process
// going away, not a defect in the job, so the error it hands back has to be
// recognisable as a cancellation — the same test the observability layer uses
// to keep a shutdown out of the error reports.
func TestErrClosedIsACancellation(t *testing.T) {
	if !errors.Is(ErrClosed, context.Canceled) {
		t.Errorf("ErrClosed = %v, want it to wrap context.Canceled", ErrClosed)
	}
}
