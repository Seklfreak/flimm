import { useEffect, useRef, useState } from "react";
import type { HLSState } from "@/lib/api";

// Scrub previews: the picture above the scrubber while you drag it.
//
// The server derives one sprite sheet per video and a WebVTT track saying
// which tile covers which seconds (`sheet.jpg#xywh=x,y,w,h`). Parsing it here
// rather than handing it to a <track> element is deliberate: a text track
// would need a video element to belong to and would fire cue events, when all
// this needs is "which rectangle for this second", answered on hover.

export interface PreviewTile {
  start: number;
  end: number;
  /** The sheet URL, resolved against the track's own URL. */
  url: string;
  x: number;
  y: number;
  w: number;
  h: number;
}

// hh:mm:ss.mmm (or mm:ss.mmm) as WebVTT writes it.
function parseTime(raw: string): number {
  const parts = raw.trim().split(":").map(Number);
  if (parts.some((n) => Number.isNaN(n))) return NaN;
  return parts.reduce((total, n) => total * 60 + n, 0);
}

/**
 * Parses a preview track. Anything malformed is skipped rather than thrown:
 * a scrubber with no pictures is a scrubber, and a broken derivation must not
 * take the player down with it.
 */
export function parsePreviewTrack(vtt: string, trackURL: string): PreviewTile[] {
  const tiles: PreviewTile[] = [];
  const lines = vtt.split(/\r?\n/);
  for (let i = 0; i < lines.length; i++) {
    const arrow = lines[i].indexOf("-->");
    if (arrow < 0) continue;
    const start = parseTime(lines[i].slice(0, arrow));
    const end = parseTime(lines[i].slice(arrow + 3).trim().split(/\s+/)[0]);
    const payload = (lines[i + 1] ?? "").trim();
    const hash = payload.indexOf("#xywh=");
    if (Number.isNaN(start) || Number.isNaN(end) || hash < 0) continue;
    const [x, y, w, h] = payload
      .slice(hash + 6)
      .split(",")
      .map(Number);
    if ([x, y, w, h].some((n) => Number.isNaN(n)) || w <= 0 || h <= 0) continue;
    tiles.push({
      start,
      end,
      url: new URL(payload.slice(0, hash), new URL(trackURL, window.location.href)).toString(),
      x,
      y,
      w,
      h,
    });
    i++;
  }
  return tiles;
}

/** The tile covering `time`, or the last one before it. */
export function tileAt(tiles: PreviewTile[], time: number): PreviewTile | undefined {
  if (tiles.length === 0) return undefined;
  let best: PreviewTile | undefined;
  for (const tile of tiles) {
    if (time >= tile.start && time < tile.end) return tile;
    if (tile.start <= time) best = tile;
  }
  return best ?? tiles[0];
}

/** What the scrubber has, and what the server last said about getting it. */
export interface PreviewStatus {
  tiles: PreviewTile[];
  /** The derivation's state as the server reports it, or `null` before the
   *  first answer and when a 404 carried nothing readable. */
  state: HLSState | null;
  /** How far through the source the derivation has got, 0–1. A sheet is one
   *  decode of the whole file, so on a long video this is the difference
   *  between a wait and a wedge. */
  progress: number;
  /** How many times the track has been asked for — the shape of the wait,
   *  which is otherwise invisible. */
  asked: number;
}

const IDLE_PREVIEW: PreviewStatus = { tiles: [], state: null, progress: 0, asked: 0 };

/**
 * Loads a video's preview track, if the server has derived one.
 *
 * A 404 is the normal answer while it is being made — asking is what starts
 * that — so this keeps asking, with growing gaps, for as long as the player is
 * open. It used to stop after three tries, which meant a sheet that took
 * longer than three quarters of a minute to derive never reached the scrubber
 * it was made for, however long the video stayed on screen. Nothing waits on
 * it either way: the player is already playing.
 *
 * `watched` says someone is *reading* the answer — the playback stats panel is
 * open — which is the one case where the gaps are the wrong shape. They grow
 * because nothing is waiting on the sheet; a percentage nobody sees move is
 * not a percentage. It only quickens a run that is actually running: a failed
 * job is restarted by the very act of asking, so hurrying that would be a
 * derivation every second and a half rather than a number.
 */
export function usePreviewTiles(trackURL: string | undefined, mediaReady: boolean, watched = false): PreviewStatus {
  const [status, setStatus] = useState<PreviewStatus>(IDLE_PREVIEW);
  // Read when the next gap is chosen rather than depended on, because opening
  // the panel must not restart the load — that would blank a sheet the
  // scrubber already has and ask the server for it again.
  const watchedRef = useRef(watched);
  watchedRef.current = watched;
  // Set by the effect below, so opening the panel can bring the next ask
  // forward instead of waiting out a minute-long gap that is already pending.
  const askNow = useRef<(() => void) | null>(null);

  useEffect(() => {
    setStatus(IDLE_PREVIEW);
    askNow.current = null;
    if (!trackURL || !mediaReady) return;
    let cancelled = false;
    let settled = false;
    let attempt = 0;
    let timer: number | undefined;

    const load = async () => {
      let pending: { state: HLSState | null; progress: number } = { state: null, progress: 0 };
      try {
        const res = await fetch(trackURL, { credentials: "include" });
        if (cancelled) return;
        if (res.ok) {
          const tiles = parsePreviewTrack(await res.text(), trackURL);
          settled = true;
          setStatus({ tiles, state: "done", progress: 1, asked: attempt + 1 });
          return;
        }
        pending = await derivationPending(res);
      } catch {
        /* offline, or the player is being torn down; the next gap asks again */
      }
      if (cancelled) return;
      setStatus({ tiles: [], ...pending, asked: attempt + 1 });
      timer = window.setTimeout(load, nextGap(attempt++, watchedRef.current, pending.state));
    };
    askNow.current = () => {
      if (cancelled || settled) return;
      window.clearTimeout(timer);
      void load();
    };
    void load();
    return () => {
      cancelled = true;
      askNow.current = null;
      if (timer) window.clearTimeout(timer);
    };
  }, [trackURL, mediaReady]);

  // Only the *opening* of the panel brings an ask forward. Firing on mount as
  // well would double the first request of every video opened with the panel
  // already up — and for a preview, asking is what starts the work.
  const wasWatched = useRef(watched);
  useEffect(() => {
    const opened = watched && !wasWatched.current;
    wasWatched.current = watched;
    if (opened) askNow.current?.();
  }, [watched]);

  return status;
}

/** How long to wait before asking again, given who is waiting for the answer. */
function nextGap(attempt: number, watched: boolean, state: HLSState | null): number {
  if (watched && state === "running") return WATCHED_GAP;
  return RETRY_GAPS[Math.min(attempt, RETRY_GAPS.length - 1)];
}

/**
 * What a 404 carries: the job's state and how far it has got. A body that is
 * not the JSON the server sends — a proxy's error page, say — is simply no
 * answer, which is what `null` and 0 are for: "asked, told nothing".
 */
async function derivationPending(res: Response): Promise<{ state: HLSState | null; progress: number }> {
  try {
    const body = (await res.json()) as { state?: unknown; progress?: unknown };
    const state = typeof body?.state === "string" && STATES.includes(body.state as HLSState) ? (body.state as HLSState) : null;
    const raw = body?.progress;
    const progress = typeof raw === "number" && Number.isFinite(raw) ? Math.min(Math.max(raw, 0), 1) : 0;
    return { state, progress };
  } catch {
    return { state: null, progress: 0 };
  }
}

const STATES: HLSState[] = ["pending", "running", "done", "failed"];

// How long to wait before asking again, in ms. A sheet is one decode of the
// whole file, so the gaps grow; the last one is then held, and asking stops
// only when the player closes.
const RETRY_GAPS = [4000, 10000, 30000, 60000];
// The gap while the stats panel is open. Fast enough that the percentage
// visibly moves, and still one small request rather than a stream of them.
const WATCHED_GAP = 1500;
