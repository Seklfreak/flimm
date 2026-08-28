import { useEffect } from "react";
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
 * Which line cues sit on, counted from the bottom (WebVTT's own convention).
 * The browser default is the last line, tight against the bottom edge and
 * behind the control bar whenever it is up; -3 leaves about two lines of
 * clearance without pushing captions into the middle of the picture. The
 * Apple clients pad their own overlay off the bottom for the same reason.
 */
export const CUE_LINE = -3;

/**
 * Lifts cues off the bottom edge. Only cues that left their position to the
 * browser are moved — a VTT that places a cue itself (on-screen text it is
 * dodging, say) means it, and is left alone.
 */
export function useCueLift(el: HTMLVideoElement | null): void {
  useEffect(() => {
    if (!el) return;
    const tracks = el.textTracks;
    const lift = (track: TextTrack) => {
      for (const cue of Array.from(track.cues ?? [])) {
        const vtt = cue as VTTCue;
        if (vtt.line === "auto") vtt.line = CUE_LINE;
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
    return () => {
      tracks.removeEventListener?.("addtrack", attach);
      tracks.removeEventListener?.("change", attach);
      for (let i = 0; i < tracks.length; i++) tracks[i].removeEventListener?.("cuechange", onCueChange);
    };
  }, [el]);
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
