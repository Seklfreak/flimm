import { useEffect, useRef } from "react";
import { useQueryClient, type QueryClient } from "@tanstack/react-query";
import { api, sendProgressBeacon, type HistoryEntry, type Page, type Video } from "@/lib/api";
import { keys } from "@/lib/queries";

// Keep the sidebar's continue-watching rail honest while playback runs: each
// heartbeat patches the cached entry in place (position, freshness order)
// instead of waiting for a refetch the always-mounted sidebar never triggers.
// A video not on the rail yet appears on the next server read — the query is
// invalidated so that read happens.
function patchInProgressRail(qc: QueryClient, id: string, position: number) {
  const cached = qc.getQueryData<Page<HistoryEntry>>(keys.inProgress);
  if (!cached) return;
  const index = cached.items.findIndex((e) => e.video.id === id);
  if (index < 0) {
    void qc.invalidateQueries({ queryKey: keys.inProgress });
    return;
  }
  const entry = cached.items[index];
  const updated: HistoryEntry = {
    ...entry,
    played_at: new Date().toISOString(),
    video: { ...entry.video, position, progress: entry.video.duration > 0 ? position / entry.video.duration : entry.video.progress },
  };
  const items = [updated, ...cached.items.slice(0, index), ...cached.items.slice(index + 1)];
  qc.setQueryData<Page<HistoryEntry>>(keys.inProgress, { ...cached, items });
}

// The cached detail is what the player resumes from when this page is opened
// again — the card, the rail and browser-back all land on it before any
// refetch answers — so it has to carry the position playback reached, not
// the one the page was first opened with. Patched in place, like the rail.
function patchVideoPosition(qc: QueryClient, id: string, position: number) {
  qc.setQueryData<Video>(keys.video(id), (cached) =>
    cached ? { ...cached, position, progress: cached.duration > 0 ? position / cached.duration : cached.progress } : cached,
  );
}

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
  const qc = useQueryClient();
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
      patchInProgressRail(qc, id, pos);
      patchVideoPosition(qc, id, pos);
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
      if (video.currentTime > 0) {
        patchInProgressRail(qc, id, Math.floor(video.currentTime));
        patchVideoPosition(qc, id, Math.floor(video.currentTime));
        sendProgressBeacon(id, video.currentTime, playlistIdRef.current);
      }
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
      // Leaving the page (route change) — flush the position, patch the rail
      // in place, and let the server's ordering (and any completion drop)
      // land once the beacon has: the refetch waits a beat for it.
      if (video.currentTime > 0) {
        patchInProgressRail(qc, id, Math.floor(video.currentTime));
        patchVideoPosition(qc, id, Math.floor(video.currentTime));
        sendProgressBeacon(id, video.currentTime, playlistIdRef.current);
      }
      window.setTimeout(() => void qc.invalidateQueries({ queryKey: keys.inProgress }), 1_500);
    };
  }, [video, id, qc]);
}
