import { useEffect, useState } from "react";
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
  /** How many times the track has been asked for — the shape of the wait,
   *  which is otherwise invisible. */
  asked: number;
}

const IDLE_PREVIEW: PreviewStatus = { tiles: [], state: null, asked: 0 };

/**
 * Loads a video's preview track, if the server has derived one.
 *
 * A 404 is the normal answer while it is being made — asking is what starts
 * that — so this keeps asking, with growing gaps, for as long as the player is
 * open. It used to stop after three tries, which meant a sheet that took
 * longer than three quarters of a minute to derive never reached the scrubber
 * it was made for, however long the video stayed on screen. Nothing waits on
 * it either way: the player is already playing.
 */
export function usePreviewTiles(trackURL: string | undefined, mediaReady: boolean): PreviewStatus {
  const [status, setStatus] = useState<PreviewStatus>(IDLE_PREVIEW);
  useEffect(() => {
    setStatus(IDLE_PREVIEW);
    if (!trackURL || !mediaReady) return;
    let cancelled = false;
    let attempt = 0;
    let timer: number | undefined;

    const load = async () => {
      let state: HLSState | null = null;
      try {
        const res = await fetch(trackURL, { credentials: "include" });
        if (cancelled) return;
        if (res.ok) {
          const tiles = parsePreviewTrack(await res.text(), trackURL);
          setStatus({ tiles, state: "done", asked: attempt + 1 });
          return;
        }
        state = await derivationState(res);
      } catch {
        /* offline, or the player is being torn down; the next gap asks again */
      }
      if (cancelled) return;
      setStatus({ tiles: [], state, asked: attempt + 1 });
      timer = window.setTimeout(load, RETRY_GAPS[Math.min(attempt++, RETRY_GAPS.length - 1)]);
    };
    void load();
    return () => {
      cancelled = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [trackURL, mediaReady]);
  return status;
}

/**
 * The job state a 404 carries. A body that is not the JSON the server sends —
 * a proxy's error page, say — is simply no answer, which is what `null` is
 * for: "asked, told nothing".
 */
async function derivationState(res: Response): Promise<HLSState | null> {
  try {
    const body: unknown = await res.json();
    const state = (body as { state?: unknown })?.state;
    return typeof state === "string" && STATES.includes(state as HLSState) ? (state as HLSState) : null;
  } catch {
    return null;
  }
}

const STATES: HLSState[] = ["pending", "running", "done", "failed"];

// How long to wait before asking again, in ms. A sheet is one decode of the
// whole file, so the gaps grow; the last one is then held, and asking stops
// only when the player closes.
const RETRY_GAPS = [4000, 10000, 30000, 60000];
