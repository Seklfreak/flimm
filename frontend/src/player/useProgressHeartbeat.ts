import { useEffect, useRef } from "react";
import { api, sendProgressBeacon } from "@/lib/api";

// POST /videos/:id/progress every 10 s while playing, on pause/seek, and on
// unload (keepalive fetch). Reports `watched` flips back via onWatched.
//
// `playlistId` is the play context (the playlist the video is being played
// *from*, per PlayContext), not the video's playlist membership — it must be
// carried on every heartbeat so the server can tell when playback is from a
// music playlist and skip recording watch state accordingly. Every path here
// (interval, pause, seek, ended, pagehide/route-leave) passes it through.
export function useProgressHeartbeat(
  video: HTMLVideoElement | null,
  id: string,
  onWatched?: () => void,
  playlistId?: string,
) {
  const lastSent = useRef(-1);
  const onWatchedRef = useRef(onWatched);
  onWatchedRef.current = onWatched;
  const playlistIdRef = useRef(playlistId);
  playlistIdRef.current = playlistId;

  useEffect(() => {
    if (!video) return;
    let timer: number | undefined;

    const send = (force = false) => {
      const pos = Math.floor(video.currentTime);
      if (!force && pos === lastSent.current) return;
      if (pos <= 0 && !force) return;
      lastSent.current = pos;
      api
        .progress(id, pos, playlistIdRef.current)
        .then((r) => {
          if (r.watched) onWatchedRef.current?.();
        })
        .catch(() => {
          /* heartbeat loss is fine; the next one catches up */
        });
    };
    const start = () => {
      stop();
      timer = window.setInterval(() => send(), 10_000);
    };
    const stop = () => {
      if (timer) window.clearInterval(timer);
      timer = undefined;
    };
    const onPause = () => {
      stop();
      send(true);
    };
    const onSeeked = () => send(true);
    const onUnload = () => {
      if (video.currentTime > 0) sendProgressBeacon(id, video.currentTime, playlistIdRef.current);
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") onUnload();
    };

    video.addEventListener("play", start);
    video.addEventListener("pause", onPause);
    video.addEventListener("seeked", onSeeked);
    video.addEventListener("ended", onPause);
    window.addEventListener("pagehide", onUnload);
    document.addEventListener("visibilitychange", onVisibility);
    if (!video.paused) start();
    return () => {
      stop();
      video.removeEventListener("play", start);
      video.removeEventListener("pause", onPause);
      video.removeEventListener("seeked", onSeeked);
      video.removeEventListener("ended", onPause);
      window.removeEventListener("pagehide", onUnload);
      document.removeEventListener("visibilitychange", onVisibility);
      // Leaving the page (route change) — flush the position.
      if (video.currentTime > 0) sendProgressBeacon(id, video.currentTime, playlistIdRef.current);
    };
  }, [video, id]);
}
