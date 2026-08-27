// Command fake-ta runs a stand-in TubeArchivist for local development.
//
// It serves the subset of TA's API that Flimm calls over a small fixed
// catalogue, and generates the media files with ffmpeg on first run so videos
// really play, seek and resume. Point the backend at it:
//
//	go run ./cmd/fake-ta &
//	TA_URL=http://localhost:8001 TA_TOKEN=dev AUTH_DISABLED=true … go run ./cmd/server
//
// Nothing it holds is persisted beyond the generated files, and no watch state
// ever reaches a real archive.
package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Seklfreak/flimm/internal/faketa"
)

func main() {
	addr := flag.String("addr", ":8001", "listen address")
	mediaDir := flag.String("media-dir", filepath.Join(os.TempDir(), "flimm-fake-ta"), "where generated media files are kept")
	ffmpegPath := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary used to generate media")
	verbose := flag.Bool("v", false, "log every request")
	flag.Parse()

	level := slog.LevelInfo
	if *verbose {
		level = slog.LevelDebug
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	catalogue := faketa.NewCatalogue()
	media := faketa.NewMedia(*mediaDir, *ffmpegPath, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("generating media", "dir", *mediaDir, "videos", len(catalogue.Videos))
	if err := media.Generate(ctx, catalogue); err != nil {
		log.Error("media generation failed", "err", err)
		log.Error("ffmpeg is required; pass -ffmpeg or install it")
		os.Exit(1)
	}

	server := &http.Server{
		Addr:              *addr,
		Handler:           faketa.NewServer(catalogue, media, log).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("fake TubeArchivist listening",
		"addr", *addr, "channels", len(catalogue.Channels), "videos", len(catalogue.Videos))

	serveErr := make(chan error, 1)
	go func() { serveErr <- server.ListenAndServe() }()
	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server failed", "err", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}
}
