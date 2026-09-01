import { useEffect } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type Loudness } from "../lib/api";

// Loudness normalisation, on the web player's side.
//
// The server measures the video and says how many decibels to move it by; all
// this does is ask, wait for the answer, and set the element's volume. The
// gain is never positive — see the note on the server — so a plain
// `HTMLMediaElement.volume`, which cannot go above 1, is enough, and no
// WebAudio graph has to sit between the player and the speakers.

/** A gain in decibels as the linear scale `volume` takes. */
export function volumeFor(gainDB: number | undefined): number {
  if (!gainDB || !Number.isFinite(gainDB) || gainDB >= 0) return 1;
  return Math.max(Math.pow(10, gainDB / 20), 0);
}

/** How often to ask while the pass runs, and while someone is watching it. */
const POLL = 4000;
const WATCHED_POLL = 1500;

/**
 * Asks for a video's measurement, and keeps asking while it is being made.
 *
 * The first request is what starts the analysis pass, so this polls until
 * there is an answer: a measurement that is not ready yet will be shortly, and
 * playing at the archived level in the meantime is exactly what the player did
 * before the feature existed.
 *
 * `watched` quickens it while the playback stats panel is showing the pass's
 * progress — a number that moves once every four seconds is a number nobody
 * can tell from a stuck one. Only the panel's own reader passes it, so the
 * gain's reader keeps the ordinary cadence.
 */
export function useLoudness(videoId: string, enabled: boolean, watched = false) {
  return useQuery({
    queryKey: ["videos", videoId, "loudness"],
    queryFn: () => api.loudness(videoId),
    enabled,
    staleTime: Infinity,
    retry: false,
    refetchInterval: (query) => {
      const state = (query.state.data as Loudness | undefined)?.state;
      if (state !== "pending" && state !== "running") return false;
      return watched ? WATCHED_POLL : POLL;
    },
  });
}

/**
 * Plays a video at the level the server measured for it.
 *
 * Volume is set rather than remembered: nothing else in this player writes it,
 * and the viewer's own control is the mute button and the system volume. When
 * normalisation is off, or nothing has been measured, it goes back to 1 — a
 * preference turned off has to be audible immediately, not on the next video.
 */
export function useLoudnessGain(
  el: HTMLMediaElement | null,
  videoId: string,
  enabled: boolean,
) {
  const { data } = useLoudness(videoId, enabled);
  const gain = enabled ? data?.gain_db : undefined;
  useEffect(() => {
    if (!el) return;
    el.volume = volumeFor(gain);
  }, [el, gain]);
  return gain ?? 0;
}
