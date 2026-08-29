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

/**
 * Asks for a video's measurement, and keeps asking while it is being made.
 *
 * The first request is what starts the analysis pass, so this polls a few
 * times and then settles: a measurement that is not ready this time will be
 * next time, and playing at the archived level in the meantime is exactly what
 * the player did before the feature existed.
 */
export function useLoudness(videoId: string, enabled: boolean) {
  return useQuery({
    queryKey: ["videos", videoId, "loudness"],
    queryFn: () => api.loudness(videoId),
    enabled,
    staleTime: Infinity,
    retry: false,
    refetchInterval: (query) => {
      const state = (query.state.data as Loudness | undefined)?.state;
      return state === "pending" || state === "running" ? 4000 : false;
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
