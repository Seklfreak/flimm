# Roadmap

## Next

Nothing in flight. Items are promoted here from **Ideas** below when they
are picked up.

## Ideas

- **Per-category SponsorBlock skips and a manual skip button** — segments are
  tinted on the timeline already; choosing what to skip automatically per
  category, and skipping one by hand, are not built.
- **Silence and black-frame detection** — an intro/outro heuristic for the long
  tail of videos SponsorBlock has never seen, derived locally.
- **Transcripts (Whisper)** — generate subtitles for videos the archive has
  none for, and index them for search *inside* a video. The heaviest item here
  by a wide margin, and the only one that unlocks a new kind of search.
- **"Most replayed" heatmap** — yt-dlp exposes YouTube's heatmap, which would
  tint the scrubber beside the SponsorBlock segments. It needs a live YouTube
  request per video, though, so the honest place to capture it is at download
  time on the TubeArchivist side, not a per-view fetch from Flimm.
- **Download queue management** — view and manage TubeArchivist's download
  queue and subscriptions from Flimm (add URLs, retry, ignore).
- **Multi-instance** — one Flimm backend fronting several TubeArchivist
  instances, with feeds spanning them.
