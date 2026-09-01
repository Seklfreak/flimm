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

func write(t *testing.T, dst string, n int) error {
	t.Helper()
	return os.WriteFile(dst, make([]byte, n), 0o600)
}

func TestCacheDerivesOnceThenServesFromDisk(t *testing.T) {
	c, err := NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	derive := func(_ context.Context, dst string) error {
		calls.Add(1)
		return write(t, dst, 10)
	}
	for range 3 {
		if _, err := c.Get(t.Context(), "audio-a.webm", derive); err != nil {
			t.Fatal(err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("derived %d times, want 1 — later requests must hit the cache", got)
	}
}

// Several listeners opening the same track must not each spawn ffmpeg.
func TestCacheCollapsesConcurrentDerivations(t *testing.T) {
	c, err := NewCache(t.TempDir(), 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	release := make(chan struct{})
	derive := func(_ context.Context, dst string) error {
		calls.Add(1)
		<-release
		return write(t, dst, 10)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = c.Get(t.Context(), "audio-a.webm", derive)
		}()
	}
	// Let every goroutine reach Get before the derivation completes.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("derived %d times concurrently, want 1", got)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
}

// A failed derivation must leave nothing behind, so the next request retries
// instead of serving a truncated file.
func TestCacheDoesNotPublishFailedDerivation(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 0, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("ffmpeg exploded")
	_, err = c.Get(t.Context(), "audio-a.webm", func(_ context.Context, dst string) error {
		_ = write(t, dst, 5) // partial output, as a killed ffmpeg would leave
		return want
	})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "audio-a.webm")); !os.IsNotExist(err) {
		t.Error("a failed derivation left a file in the cache")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 250, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if _, err := c.Get(t.Context(), name, func(_ context.Context, dst string) error { return write(t, dst, 100) }); err != nil {
			t.Fatal(err)
		}
		// Distinct timestamps so "least recently used" is well defined.
		time.Sleep(10 * time.Millisecond)
	}
	// a is oldest and the cap is 250 bytes for 300 bytes of content.
	if _, err := os.Stat(filepath.Join(dir, "a")); !os.IsNotExist(err) {
		t.Error("oldest entry survived eviction")
	}
	for _, name := range []string{"b", "c"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("recent entry %q was evicted", name)
		}
	}
}

// Reading an entry must refresh its position in the LRU order.
func TestCacheTouchOnHitProtectsFromEviction(t *testing.T) {
	dir := t.TempDir()
	c, err := NewCache(dir, 250, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	mk := func(_ context.Context, dst string) error { return write(t, dst, 100) }
	for _, name := range []string{"a", "b"} {
		if _, err := c.Get(t.Context(), name, mk); err != nil {
			t.Fatal(err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := c.Get(t.Context(), "a", mk); err != nil { // touches a
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if _, err := c.Get(t.Context(), "c", mk); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "a")); err != nil {
		t.Error("recently read entry was evicted")
	}
	if _, err := os.Stat(filepath.Join(dir, "b")); !os.IsNotExist(err) {
		t.Error("least recently used entry survived")
	}
}

// The sweep can only clean what it can attribute, and it deletes what it
// attributes — so a name read wrongly is either a rendition that never goes
// away or one that goes away while someone is watching it.
func TestEntryVideoReadsEveryLayoutThisPackageWrites(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	for _, name := range []string{
		LoudnessVariant + "-" + id,
		AudioVariant + "-" + id + AudioExt,
		AudioAACVariant + "-" + id + AudioAACExt,
		PreviewVariant + "-" + id,
		HLSName(id, 720),
		HLSName(id, 2160),
		FrameVariant + "-" + id + "-4200" + FrameExt,
	} {
		if _, got := EntryOf(name); got != id {
			t.Errorf("EntryOf(%q) video = %q, want %q", name, got, id)
		}
	}

	// An id with dashes in it is the case a "text after the last dash" reading
	// would get wrong, and YouTube ids do contain them.
	const dashed = "a-b_c-d_efg"
	if _, got := EntryOf(HLSName(dashed, 480)); got != dashed {
		t.Errorf("EntryOf a dashed id = %q, want %q", got, dashed)
	}
	if _, got := EntryOf(FrameVariant + "-" + dashed + "-1000" + FrameExt); got != dashed {
		t.Errorf("EntryOf a dashed frame = %q, want %q", got, dashed)
	}

	for _, name := range []string{"", ".complete", "junk", "sheet.jpg", ".tmp-123"} {
		if v, got := EntryOf(name); v != "" || got != "" {
			t.Errorf("EntryOf(%q) = (%q, %q), want nothing", name, v, got)
		}
	}
}

// Only the renditions are worth reclaiming. A loudness measurement is a few
// hundred bytes and a full audio decode to rebuild; deleting it frees nothing
// and costs that decode the next time the video is opened.
func TestOnlyRenditionsAreReclaimable(t *testing.T) {
	const id = "dQw4w9WgXcQ"
	for _, name := range []string{HLSName(id, 720), AudioVariant + "-" + id + AudioExt, AudioAACVariant + "-" + id + AudioAACExt} {
		if !Reclaimable(name) {
			t.Errorf("%q should be swept: it is the size that makes a cleanup worth running", name)
		}
	}
	for _, name := range []string{
		LoudnessVariant + "-" + id,
		PreviewVariant + "-" + id,
		FrameVariant + "-" + id + "-4200" + FrameExt,
		".complete", "junk",
	} {
		if Reclaimable(name) {
			t.Errorf("%q should be kept: sweeping it frees nothing and costs a decode", name)
		}
	}
}

// Cleaning up after a watched video means every derivation of it, and nothing
// belonging to a video still being watched.
func TestRemoveForTakesOneVideosDerivationsOnly(t *testing.T) {
	c := newDirCache(t, 0, 1)
	const gone, kept = "aaaaaaaaaaa", "bbbbbbbbbbb"

	for _, name := range []string{HLSName(gone, 720), HLSName(gone, 480), AudioVariant + "-" + gone + AudioExt, LoudnessVariant + "-" + gone} {
		if _, err := c.Get(t.Context(), name, func(_ context.Context, dst string) error { return write(t, dst, 100) }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Get(t.Context(), HLSName(kept, 720), func(_ context.Context, dst string) error { return write(t, dst, 100) }); err != nil {
		t.Fatal(err)
	}

	entries, freed := c.RemoveFor(map[string]bool{gone: true})
	if entries != 3 {
		t.Errorf("removed %d entries, want the three renditions derived from %q", entries, gone)
	}
	if freed != 300 {
		t.Errorf("freed %d bytes, want 300", freed)
	}
	if _, err := os.Stat(c.Dir(LoudnessVariant + "-" + gone)); err != nil {
		t.Error("the loudness measurement was swept; it frees nothing and costs a decode")
	}
	if _, err := os.Stat(c.Dir(HLSName(kept, 720))); err != nil {
		t.Error("a video nobody asked to clean up lost its rendition")
	}
	if got := c.Videos(); len(got) != 1 || got[0] != kept {
		t.Errorf("cache holds %v, want only %q", got, kept)
	}
}

// A rendition being written is being read: a viewer is watching it right now,
// whatever the watch history said when the sweep started.
func TestRemoveForLeavesARunningJobAlone(t *testing.T) {
	c := newDirCache(t, 0, 1)
	const id = "ccccccccccc"
	started, finish := make(chan struct{}), make(chan struct{})
	c.StartDir(HLSName(id, 720), func(_ context.Context, dir string) error {
		close(started)
		<-finish
		return writeIn(t, dir, "index.m3u8", 10)
	})
	<-started
	defer close(finish)

	if entries, _ := c.RemoveFor(map[string]bool{id: true}); entries != 0 {
		t.Errorf("removed %d entries, want none while the job runs", entries)
	}
	if _, err := os.Stat(c.Dir(HLSName(id, 720))); err != nil {
		t.Error("the entry a job is writing was removed")
	}
}
