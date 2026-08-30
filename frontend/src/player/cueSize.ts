import { useEffect, useRef } from "react";
import type { Prefs } from "@/lib/api";

// Subtitle cues are sized in absolute pixels, measured off the player box.
//
// They used to be sized in `em` (`video.cc-medium::cue { font-size: 0.7em }`),
// which relied on Chrome resolving that against its own default cue size —
// 5% of the video's height — so the three settings scaled with the player and
// in fullscreen. Chrome now resolves it against the video element's inherited
// font-size instead, which is a flat 16px: "medium" came out at 11px on a
// 1260px-wide player and stayed 11px in fullscreen.
//
// So the scale is applied here rather than left to the browser. The factors
// are the old ones multiplied through Chrome's 5% (0.55/0.7/0.9 em × 0.05),
// which is what these settings were always meant to mean.

export const CUE_SCALE: Record<Prefs["subtitle_size"], number> = {
  small: 0.0275,
  medium: 0.035,
  large: 0.045,
};

/**
 * Below these, cues stop being readable at all — a small window should get
 * subtitles that are proportionally too big rather than ones nobody can read.
 * One floor per setting rather than one for all three: a shared floor makes
 * "small" and "medium" the same size in a small window, which reads as a
 * broken preference.
 */
export const MIN_CUE_PX: Record<Prefs["subtitle_size"], number> = {
  small: 11,
  medium: 14,
  large: 18,
};

/** The cue font size, in px, for a player box `height` px tall. */
export function cueFontSize(height: number, size: Prefs["subtitle_size"]): number {
  const scaled = height > 0 ? height * CUE_SCALE[size] : 0;
  return Math.round(Math.max(MIN_CUE_PX[size], scaled));
}

/**
 * Which line cues sit on with nothing drawn over the video, counted from the
 * bottom (WebVTT's own convention). The browser default is the last line,
 * tight against the bottom edge; -3 leaves about two lines of clearance, which
 * is roughly the tenth of the picture the Apple clients lift their own overlay
 * by (`SubtitleLift` in FlimmKit).
 */
export const CUE_LINE = -3;

/** A cue's line box, as a multiple of its font size. */
const LINE_BOX = 1.3;
/** Between the top of the control bar and the bottom of the cue. */
const CHROME_GAP = 8;

/**
 * Which line cues sit on while the controls are up — which, whenever playback
 * is paused, is always.
 *
 * `CUE_LINE` is not enough for that: it lifts cues by about two line heights,
 * some 36px on a 306px-tall player, and the control bar there is 73px. The
 * captions a viewer paused in order to read were sitting behind it. Since
 * `line` counts in line boxes rather than pixels, this converts: how many of
 * them the chrome covers, plus one so the cue clears it rather than touches
 * it.
 *
 * Measured rather than assumed for the same reason the Apple clients measure
 * theirs — the bar is one height in a window, another in fullscreen, and the
 * cue is one size at "small" and another at "large".
 */
export function cueLineOverChrome(chromeHeight: number, cueFontPx: number): number {
  if (chromeHeight <= 0 || cueFontPx <= 0) return CUE_LINE;
  const covered = Math.ceil((chromeHeight + CHROME_GAP) / (cueFontPx * LINE_BOX));
  return Math.min(CUE_LINE, -(covered + 1));
}

/**
 * Lifts cues off the bottom edge, to `line`. Only cues that left their position
 * to the browser are moved — a VTT that places a cue itself (on-screen text it
 * is dodging, say) means it, and is left alone.
 *
 * Which is why the moved ones are remembered: after the first move a cue's
 * `line` is a number like any authored one, and the controls coming up has to
 * move it again. Without the record, the second pass could not tell its own
 * work from the VTT's.
 */
export function useCueLift(el: HTMLVideoElement | null, line: number = CUE_LINE): void {
  const moved = useRef(new WeakSet<VTTCue>());
  useEffect(() => {
    if (!el) return;
    const ours = moved.current;
    const tracks = el.textTracks;
    const lift = (track: TextTrack) => {
      for (const cue of Array.from(track.cues ?? [])) {
        const vtt = cue as VTTCue;
        if (vtt.line !== "auto" && !ours.has(vtt)) continue;
        vtt.line = line;
        ours.add(vtt);
      }
    };
    // Cues arrive with the .vtt, long after this runs, so the first cuechange
    // is where a freshly loaded track gets lifted.
    const onCueChange = (e: Event) => lift(e.target as TextTrack);
    const attach = () => {
      for (let i = 0; i < tracks.length; i++) {
        // jsdom's TextTrack is a stub without the event methods; the optional
        // calls keep the player mountable in tests.
        tracks[i].addEventListener?.("cuechange", onCueChange);
        lift(tracks[i]);
      }
    };
    attach();
    // A language switch mounts a new <track>; re-running attach is safe
    // because the same listener on the same track is added only once.
    tracks.addEventListener?.("addtrack", attach);
    tracks.addEventListener?.("change", attach);
    // The .vtt arrives after this effect runs, so at `attach` time the track
    // usually holds no cues at all. Without this the first ones are positioned
    // only when playback reaches them — which never happens for someone who
    // opens a video and pauses before the first line.
    const elements = Array.from(el.querySelectorAll("track"));
    for (const track of elements) track.addEventListener("load", attach);
    return () => {
      for (const track of elements) track.removeEventListener("load", attach);
      tracks.removeEventListener?.("addtrack", attach);
      tracks.removeEventListener?.("change", attach);
      for (let i = 0; i < tracks.length; i++) tracks[i].removeEventListener?.("cuechange", onCueChange);
    };
  }, [el, line]);
}

const STYLE_ID = "flimm-cue-size";

function cueStyle(): HTMLStyleElement {
  const existing = document.getElementById(STYLE_ID);
  if (existing) return existing as HTMLStyleElement;
  const style = document.createElement("style");
  style.id = STYLE_ID;
  document.head.appendChild(style);
  return style;
}

/**
 * Keeps the cue size in step with the player box: a resize, a size-class flip
 * and entering fullscreen all change the element's height, and all three reach
 * us through the same observer.
 *
 * One rule for the document rather than one per element — the app has a single
 * player at a time, and `::cue` cannot be styled inline.
 */
export function useCueSize(el: HTMLVideoElement | null, size: Prefs["subtitle_size"]): void {
  useEffect(() => {
    if (!el) return;
    const style = cueStyle();
    const apply = () => {
      style.textContent = `video::cue { font-size: ${cueFontSize(el.getBoundingClientRect().height, size)}px; }`;
    };
    apply();
    // jsdom has no ResizeObserver; the initial size still applies without it.
    if (typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(apply);
    observer.observe(el);
    return () => observer.disconnect();
  }, [el, size]);

  // The rule outlives a single mount only as long as the player does: leaving
  // the watch page takes the <style> with it.
  useEffect(() => {
    return () => document.getElementById(STYLE_ID)?.remove();
  }, []);
}
