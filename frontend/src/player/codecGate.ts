import type { HLSCodec, HLSVariant, StreamInfo, Video } from "@/lib/api";

/**
 * What to play, decided from `streams` before a source is handed to `<video>`.
 *
 * The archive holds whatever was downloaded — often AV1 or VP9, which some
 * browsers decode and others do not, and which no browser decodes on hardware
 * that lacks the decoder. A stalled `<video>` says nothing useful about that,
 * so the decision is made up front: play the archived file when this browser
 * can decode it, and otherwise the server's compatible rendition. This is the
 * same rule the Apple apps run (`FlimmKit/Playback/CodecGate.swift`), so a
 * viewer gets the same answer on every platform.
 */

// ---- codec names ------------------------------------------------------------

export type CodecFamily = "h264" | "hevc" | "av1" | "vp9" | "vp8" | "unknown";

/**
 * TubeArchivist reports short codec names (`avc1`, `vp09`, `av01`, `mp4a`,
 * `opus`) and the HLS ladder reports `h264` / `hevc`. Both funnel into one
 * family so there is a single table of MIME strings below.
 */
export function codecFamily(codec: string): CodecFamily {
  const c = codec.toLowerCase().trim();
  if (c.startsWith("avc1") || c.startsWith("avc3") || c.startsWith("h264") || c.startsWith("x264")) return "h264";
  if (c.startsWith("hvc1") || c.startsWith("hev1") || c.startsWith("hevc") || c.startsWith("h265")) return "hevc";
  if (c.startsWith("av01") || c.startsWith("av1")) return "av1";
  if (c.startsWith("vp09") || c.startsWith("vp9")) return "vp9";
  if (c.startsWith("vp08") || c.startsWith("vp8")) return "vp8";
  return "unknown";
}

/** Human label for the quality menu — the codec as a viewer would name it. */
export function codecLabel(codec: string): string {
  switch (codecFamily(codec)) {
    case "h264":
      return "H.264";
    case "hevc":
      return "HEVC";
    case "av1":
      return "AV1";
    case "vp9":
      return "VP9";
    case "vp8":
      return "VP8";
    default:
      return codec.toUpperCase();
  }
}

/**
 * A probe per family. Each is a concrete, decodable profile rather than a bare
 * codec name, because that is the only form both `MediaSource.isTypeSupported`
 * and `canPlayType` answer honestly:
 *
 * - `av01.0.08M.08` — Main profile, level 4.0, 8-bit.
 * - `vp09.00.10.08` — profile 0, level 1.0, 8-bit.
 * - `avc1.64001f`   — High@3.1, the floor every browser clears.
 * - `hvc1.1.6.L93.B0` — Main@L3.1, and `hvc1` because that is what the
 *   renditions are tagged with.
 *
 * VP9 and AV1 are probed in WebM as well as MP4: the archive uses either
 * container and a decoder is a decoder, but some browsers only admit to the
 * one they ship a demuxer for.
 */
const PROBES: Record<Exclude<CodecFamily, "unknown">, string[]> = {
  h264: ['video/mp4; codecs="avc1.64001f"'],
  hevc: ['video/mp4; codecs="hvc1.1.6.L93.B0"'],
  av1: ['video/mp4; codecs="av01.0.08M.08"', 'video/webm; codecs="av01.0.08M.08"'],
  vp9: ['video/mp4; codecs="vp09.00.10.08"', 'video/webm; codecs="vp9"'],
  vp8: ['video/webm; codecs="vp8"'],
};

/**
 * Whether this browser can play one MIME string.
 *
 * Two questions, either of which is a yes: Media Source Extensions (what
 * hls.js feeds, and the more precise of the two) and the element itself
 * (`canPlayType`, which is how Safari admits to codecs it decodes natively but
 * has no MSE for). Both are absent in jsdom and in a non-browser context, so
 * both are guarded — a probe that cannot run answers "no", and the caller
 * decides what that means.
 */
export function supportsMime(mime: string): boolean {
  const ms = (globalThis as { MediaSource?: { isTypeSupported?: (t: string) => boolean } }).MediaSource;
  try {
    if (ms?.isTypeSupported?.(mime)) return true;
  } catch {
    /* an implementation that throws on a malformed type is a "no" */
  }
  try {
    if (typeof document === "undefined") return false;
    return document.createElement("video").canPlayType(mime) !== "";
  } catch {
    return false;
  }
}

/** Whether Media Source Extensions can drive hls.js here. hls.js needs MSE
 *  (or a Managed Media Source) plus an fMP4/H.264 source buffer; where that
 *  exists — every desktop Chrome/Firefox/Edge and desktop Safari — hls.js is
 *  the path, because it actually plays the stream. */
function supportsMSE(): boolean {
  try {
    const g = globalThis as {
      MediaSource?: { isTypeSupported?: (t: string) => boolean };
      ManagedMediaSource?: { isTypeSupported?: (t: string) => boolean };
    };
    const ms = g.MediaSource ?? g.ManagedMediaSource;
    return ms?.isTypeSupported?.('video/mp4; codecs="avc1.42E01E,mp4a.40.2"') ?? false;
  } catch {
    return false;
  }
}

/**
 * Whether to hand HLS to the `<video>` element directly instead of hls.js.
 *
 * Only when the browser plays HLS natively **and** cannot run hls.js — i.e.
 * iOS, where there is no Media Source to build a stream on. Desktop Chrome
 * reports `canPlayType('application/vnd.apple.mpegurl')` as `"maybe"` but does
 * not actually play an HLS playlist assigned to `video.src`; it needs hls.js,
 * so MSE support wins over the `canPlayType` hint. This mirrors hls.js's own
 * `Hls.isSupported()`-first rule without importing it just to decide.
 */
export function supportsNativeHLS(): boolean {
  try {
    if (typeof document === "undefined") return false;
    const v = document.createElement("video");
    const canPlay = v.canPlayType("application/vnd.apple.mpegurl") !== "" || v.canPlayType("application/x-mpegURL") !== "";
    return canPlay && !supportsMSE();
  } catch {
    return false;
  }
}

// ---- device capabilities ----------------------------------------------------

/**
 * What *this* browser can do with the quality ladder: how tall a picture the
 * screen can actually show, and which families it decodes. A value rather than
 * a set of lookups, so the rule below can be tested for a 4K screen, a laptop
 * and a browser without an AV1 decoder without running on one.
 */
export interface DeviceCapabilities {
  /** The screen's vertical resolution in device pixels. */
  screenHeight: number;
  decodes: Record<Exclude<CodecFamily, "unknown">, boolean>;
  /** HLS without hls.js. Only affects *how* a rendition is loaded, not which. */
  nativeHLS: boolean;
}

/** 1080p is the rung every video offers — what to assume off-screen. */
export const FALLBACK_SCREEN_HEIGHT = 1080;

/**
 * Whether the probes mean anything here. Every real browser says yes to plain
 * H.264 in MP4; an environment that says no to that cannot answer at all
 * (jsdom, a headless render), and must not be read as "this plays nothing".
 */
function probesAnswer(): boolean {
  return supportsMime(PROBES.h264[0]) || supportsMime("video/mp4");
}

export function detectCapabilities(): DeviceCapabilities {
  const dpr = typeof window !== "undefined" && window.devicePixelRatio > 0 ? window.devicePixelRatio : 1;
  const px = typeof window !== "undefined" && window.screen?.height ? Math.round(window.screen.height * dpr) : 0;
  const screenHeight = px > 0 ? px : FALLBACK_SCREEN_HEIGHT;
  if (!probesAnswer()) {
    // Nothing to go on: treat every codec as playable and let the element
    // refuse what it must. The alternative is a codec wall on a browser that
    // was simply never asked.
    return {
      screenHeight,
      decodes: { h264: true, hevc: true, av1: true, vp9: true, vp8: true },
      nativeHLS: supportsNativeHLS(),
    };
  }
  return {
    screenHeight,
    decodes: {
      h264: true,
      hevc: PROBES.hevc.some(supportsMime),
      av1: PROBES.av1.some(supportsMime),
      vp9: PROBES.vp9.some(supportsMime),
      vp8: PROBES.vp8.some(supportsMime),
    },
    nativeHLS: supportsNativeHLS(),
  };
}

/**
 * Whether one archived stream decodes here. An unrecognised codec counts as
 * playable: a name nobody has taught this table is unknown, not unplayable,
 * and letting `<video>` refuse it is better than hiding a video that works.
 */
export function canDecode(stream: StreamInfo, caps: DeviceCapabilities): boolean {
  const family = codecFamily(stream.codec);
  if (family === "unknown") return true;
  return caps.decodes[family];
}

/** Whether a ladder rung's codec plays here. Same "unknown stays in" rule. */
export function canPlayVariant(codec: HLSCodec, caps: DeviceCapabilities): boolean {
  const family = codecFamily(codec);
  if (family === "unknown") return true;
  return caps.decodes[family];
}

// ---- the quality preference -------------------------------------------------

/**
 * Which rendition the viewer asked for on *this* device. Deliberately not a
 * server preference — quality is a property of the screen and the network in
 * front of it, so it is kept in `localStorage`, never in `PATCH /me/prefs`.
 */
export type QualityPreference = "auto" | number;

/** The rungs the ladder can hold, tallest first (docs/api.md). */
export const QUALITY_HEIGHTS = [2160, 1440, 1080, 720, 480];

export const QUALITY_STORAGE_KEY = "videoQuality";

export function parseQuality(raw: string | null | undefined): QualityPreference {
  if (!raw || raw === "auto") return "auto";
  const h = Number(raw);
  return Number.isFinite(h) && h > 0 ? h : "auto";
}

export function loadQuality(): QualityPreference {
  try {
    return parseQuality(window.localStorage.getItem(QUALITY_STORAGE_KEY));
  } catch {
    return "auto"; // private mode, or storage the browser refuses
  }
}

export function saveQuality(q: QualityPreference): void {
  try {
    window.localStorage.setItem(QUALITY_STORAGE_KEY, q === "auto" ? "auto" : String(q));
  } catch {
    /* the choice just doesn't outlive the tab */
  }
}

export function qualityLabel(q: QualityPreference): string {
  return q === "auto" ? "Auto" : `${q}p`;
}

// ---- the decision -----------------------------------------------------------

/** Why a video cannot be played at all, and what is left to offer. */
export interface CodecIssue {
  /** The codec to name in the message, e.g. `vp09`. */
  videoCodec: string;
  /** True when the server offers the audio-only rendition. */
  audioAvailable: boolean;
}

export type Decision =
  /** Play `media_url`: the archive decodes here and costs the server nothing. */
  | { kind: "native"; url: string }
  /** Play a rendition: nothing decodes here, or a height was picked by hand. */
  | { kind: "hls"; url: string; variant: HLSVariant | null }
  /** No rendition (an older server), but the audio-only one is still there. */
  | { kind: "audioOnly"; issue: CodecIssue }
  /** Nothing plays here: no decoder, no rendition, no derived audio. */
  | { kind: "unplayable"; issue: CodecIssue };

export function videoStreams(video: Video): StreamInfo[] {
  return (video.streams ?? []).filter((s) => s.type === "video");
}

/**
 * Whether the archived file itself decodes here — what makes `auto` mean "the
 * source, at full quality, for free". A video the server reports no `streams`
 * for counts as playable: unknown must not read as unplayable.
 */
export function archivePlays(video: Video, caps: DeviceCapabilities): boolean {
  const streams = videoStreams(video);
  if (streams.length === 0) return true;
  return streams.some((s) => canDecode(s, caps));
}

/**
 * Which rung to play, out of the ones this browser can decode at all.
 *
 * - an explicit height: that height, or the nearest lower one offered — a
 *   1080p source has no 1440 rung, and the answer there is 1080, not nothing.
 * - `auto`: the tallest rung the screen can actually show. Anything above it
 *   is bandwidth and server time spent on pixels nobody sees.
 *
 * Either way a ladder that starts above what was asked for falls to its
 * smallest rung rather than to nothing.
 */
export function pickVariant(
  preference: QualityPreference,
  ladder: HLSVariant[] | undefined,
  caps: DeviceCapabilities,
): HLSVariant | null {
  const playable = (ladder ?? []).filter((v) => canPlayVariant(v.codec, caps)).sort((a, b) => b.height - a.height);
  if (playable.length === 0) return null;
  const ceiling = preference === "auto" ? caps.screenHeight : preference;
  return playable.find((v) => v.height <= ceiling) ?? playable[playable.length - 1];
}

/**
 * The gate, and with it the quality rule. In order:
 *
 * 1. Audio-only never touches the video track, so it is always `native` — as
 *    is a video the server reports no `streams` for.
 * 2. A decodable archive plus `auto` is `native`: the original file, full
 *    quality, no transcode.
 * 3. Otherwise the ladder decides — an explicit height even when the archive
 *    would have played, because "720p" is a request for less data.
 * 4. Nothing in the ladder (an older server, or every rung in a codec this
 *    browser lacks): the archive if it plays at all, then `hls_url`, then the
 *    wall — audio-only if the derived audio is there.
 */
export function decide(
  video: Video,
  preference: QualityPreference,
  audioOnly: boolean,
  caps: DeviceCapabilities,
): Decision {
  if (audioOnly) return { kind: "native", url: video.audio_url };
  const streams = videoStreams(video);
  if (streams.length === 0) return { kind: "native", url: video.media_url };

  const plays = streams.some((s) => canDecode(s, caps));
  if (plays && preference === "auto") return { kind: "native", url: video.media_url };
  // An explicit pick at or above the source's own height buys nothing over the
  // archive when the archive plays — skip the transcode.
  if (plays && preference !== "auto") {
    const source = Math.max(0, ...streams.map((s) => s.height));
    if (source > 0 && preference >= source) return { kind: "native", url: video.media_url };
  }

  const picked = pickVariant(preference, video.hls_variants, caps);
  if (picked) return { kind: "hls", url: picked.url, variant: picked };
  if (plays) return { kind: "native", url: video.media_url };
  if (video.hls_url) return { kind: "hls", url: video.hls_url, variant: null };
  return {
    kind: video.audio_url ? "audioOnly" : "unplayable",
    issue: { videoCodec: streams[0].codec, audioAvailable: Boolean(video.audio_url) },
  };
}
