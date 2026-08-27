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
