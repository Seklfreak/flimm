import type { HLSState, HLSVariant, Loudness } from "@/lib/api";
import type { PlaybackReason } from "./codecGate";
import type { PreviewStatus } from "./preview";

// Playback stats: what the player is actually doing, said out loud.
//
// Everything here is a *reading*, never a decision — the panel must not be
// able to disagree with the player, so it is handed the same values the player
// runs on and only formats them. The one thing it adds is the vocabulary: a
// `reason` from the codec gate and a `JobState` from the cache are contract
// words, and this is where they become sentences.

/** How the picture is being delivered. */
export type DeliveryKind = "direct" | "rendition" | "audio" | "none";

export interface Delivery {
  kind: DeliveryKind;
  /** "Direct play", "Transcoded" — the headline answer. */
  label: string;
  /** Why the gate landed there, in a clause that follows the label. */
  why: string;
}

const REASONS: Record<PlaybackReason, string> = {
  "audio-only": "audio-only mode — the video track is never fetched",
  "codecs-unknown": "the server reported no stream list, so nothing was gated",
  "archive-decodes": "this browser decodes the archived file, and quality is Auto",
  "archive-is-enough": "the quality asked for is at or above the source, so the archive is already it",
  "quality-picked": "a quality was asked for, and a rung matched it",
  "no-decoder": "this browser has no decoder for the archived file",
  "no-rung": "no rung matched, and the archived file plays here",
  "default-rendition": "this server offers no ladder, only the default rendition",
  "nothing-plays": "no decoder here, and no rendition to fall back to",
};

/**
 * The headline: what is on screen and how it got there.
 *
 * "Direct play" and "Transcoded" are the two answers worth being able to read
 * at a glance — the whole point of the panel is that a video quietly costing
 * the server an encode should not look like one that costs it nothing.
 */
export function describeDelivery(kind: "native" | "hls" | "audioOnly" | "unplayable", reason: PlaybackReason, audioOnly: boolean): Delivery {
  const why = REASONS[reason];
  if (audioOnly) return { kind: "audio", label: "Audio only", why };
  switch (kind) {
    case "native":
      return { kind: "direct", label: "Direct play", why };
    case "hls":
      return { kind: "rendition", label: "Transcoded", why };
    default:
      return { kind: "none", label: "Not playing", why };
  }
}

/** `1080p · avc1.640028`, or as much of it as is known. */
export function describeStream(height: number, codec: string): string {
  const parts: string[] = [];
  if (height > 0) parts.push(`${height}p`);
  if (codec) parts.push(codec);
  return parts.join(" · ") || "unknown";
}

/**
 * A running job's state with how far it has got attached.
 *
 * The percentage is only ever shown *while* something is running: on a
 * finished job it is 100 by definition and says nothing, and on one that has
 * not started it would be a zero the reader takes for a stall. Every
 * derivation in the panel says it this way, so "deriving · 42%" means the same
 * thing whether it came from a transcode counting its own segments or a scan
 * reading ffmpeg's clock.
 */
export function stateWithProgress(state: HLSState | null | undefined, progress: number | null | undefined): string {
  const label = stateLabel(state);
  if (state !== "running" || progress === null || progress === undefined || !(progress > 0)) return label;
  return `${label} · ${Math.round(progress * 100)}%`;
}

/**
 * A derivation's state as a person would say it.
 *
 * `pending` is the server's word for "nobody has asked", which after the
 * client has asked means the answer is simply on its way.
 */
export function stateLabel(state: HLSState | null | undefined): string {
  switch (state) {
    case "done":
      return "ready";
    case "running":
      return "deriving";
    case "failed":
      return "failed";
    case "pending":
      return "not started";
    default:
      return "waiting";
  }
}

/** The rendition line: the rung, its state, and how far the encoder has got. */
export function describeRendition(variant: HLSVariant | null, state: HLSState | null, progress: number | null): string {
  const parts: string[] = [];
  if (variant) parts.push(describeStream(variant.height, variant.codec));
  // The rendition's progress is the fraction of it that exists, which is not
  // where playback is — see api.md. It reads the same way as the scans' all the
  // same: how much of the work is done.
  parts.push(stateWithProgress(state ?? variant?.state, progress));
  return parts.join(" · ");
}

/**
 * The scrub-preview line.
 *
 * The number of asks is in it on purpose: a sheet takes one decode of the
 * whole file and can queue behind other work, so "still deriving, asked 6×"
 * is a wait, and the same line with a `failed` is a bug.
 */
export function describePreview(status: PreviewStatus, offered: boolean): string {
  if (!offered) return "not offered by this server";
  if (status.tiles.length > 0) {
    const first = status.tiles[0];
    const every = first.end - first.start;
    const grid = `${first.w}×${first.h}`;
    return `ready · ${status.tiles.length} stills, ${grid}, every ${every.toFixed(1)}s`;
  }
  if (status.asked === 0) return "not asked for yet";
  return `${stateWithProgress(status.state, status.progress)} · asked ${status.asked}×`;
}

/**
 * The loudness line.
 *
 * The gain is what actually reaches the element, so it is reported as applied
 * or not rather than as measured: a measurement that exists while the
 * preference is off changes nothing about what you hear.
 */
export function describeLoudness(loudness: Loudness | undefined, enabled: boolean): string {
  if (!enabled) return "off — playing at the archived level";
  if (!loudness) return "waiting";
  if (loudness.state !== "done") return stateWithProgress(loudness.state, loudness.progress);
  const gain = loudness.gain_db;
  const applied = gain < 0 ? `${gain.toFixed(1)} dB` : "no change";
  return `${applied} · measured ${loudness.measured_lufs.toFixed(1)} LUFS, peak ${loudness.peak_dbtp.toFixed(1)} dBTP`;
}

/**
 * Seconds of contiguous buffer ahead of where playback is.
 *
 * Only the range the playhead is *in* counts: a player that has fetched two
 * minutes somewhere else on the timeline has nothing to play, and a number
 * that says otherwise is worse than no number.
 */
export function bufferedAhead(el: { currentTime: number; buffered: TimeRanges } | null): number | null {
  if (!el) return null;
  const t = el.currentTime;
  for (let i = 0; i < el.buffered.length; i++) {
    if (t >= el.buffered.start(i) - 0.1 && t <= el.buffered.end(i)) return Math.max(0, el.buffered.end(i) - t);
  }
  return 0;
}

/** `12 of 3,410 (0.4%)`, or null when the browser will not say. */
export function describeDroppedFrames(quality: { droppedVideoFrames: number; totalVideoFrames: number } | null): string | null {
  if (!quality || quality.totalVideoFrames === 0) return null;
  const pct = (quality.droppedVideoFrames / quality.totalVideoFrames) * 100;
  return `${quality.droppedVideoFrames.toLocaleString()} of ${quality.totalVideoFrames.toLocaleString()} (${pct.toFixed(1)}%)`;
}

/** What the element is doing, in the words the spec uses. */
export const READY_STATES = ["nothing", "metadata", "current frame", "future data", "enough data"];
export const NETWORK_STATES = ["empty", "idle", "loading", "no source"];

export function elementState(readyState: number, networkState: number): string {
  return `${READY_STATES[readyState] ?? readyState} · ${NETWORK_STATES[networkState] ?? networkState}`;
}

/** The codecs this browser admits to, as a list to print. */
export function describeDecoders(decodes: Record<string, boolean>): string {
  const yes = Object.keys(decodes).filter((k) => decodes[k]);
  return yes.length > 0 ? yes.join(", ") : "none";
}
