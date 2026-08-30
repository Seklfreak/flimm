import { useEffect, useRef } from "react";
import { api } from "@/lib/api";

// Below this a "stall" is the ordinary gap between two segments, and reporting
// it would bury the ones a person noticed. The server applies the same floor;
// this one saves the request. Matches `StallReporter` on the Apple clients.
const MIN_SECONDS = 0.4;

// Reports a mid-playback buffer to `POST /videos/:id/stall`.
//
// The client is the only side that knows the picture stopped — nothing fails,
// no request errors, the viewer just watches a spinner. The server is the only
// side that knows why it might have, because it knows where the encoder had got
// to and whether the segment being waited for existed. So this says what was
// playing and for how long it stopped, and the server attributes it (see
// docs/api.md). The Apple clients do the same through FlimmKit's
// `StallReporter`; the rule about what counts as a stall is the same on both.
//
// `height` is the compatible rendition being played, or 0 when the archived
// file is playing directly — which is what lets the server tell "the transcode
// is behind the viewer" apart from "the bytes were there and something between
// here and the disk was slow".
export function useStallReport(video: HTMLVideoElement | null, id: string, height: number) {
  const heightRef = useRef(height);
  heightRef.current = height;

  useEffect(() => {
    if (!video) return;
    let began: number | null = null;

    const start = () => {
      // A paused player is not stalling, and neither is one that has not been
      // asked to play yet.
      if (began === null && !video.paused) began = performance.now();
    };
    // A stall that never ended is abandoned rather than reported: its length is
    // unknown, and the viewer may simply have left.
    const abandon = () => {
      began = null;
    };
    const end = () => {
      if (began === null) return;
      const seconds = (performance.now() - began) / 1000;
      began = null;
      if (seconds < MIN_SECONDS) return;
      // Best effort by design: a report that fails is not worth telling a
      // viewer about, and never worth retrying into a server that is already
      // having a bad time.
      api
        .reportStall(id, {
          position: video.currentTime,
          seconds,
          height: heightRef.current,
          client: "web",
        })
        .catch(() => {});
    };

    video.addEventListener("waiting", start);
    video.addEventListener("stalled", start);
    video.addEventListener("playing", end);
    video.addEventListener("pause", abandon);
    video.addEventListener("ended", abandon);
    // A seek into an unbuffered part is the viewer asking to wait, not the
    // stream failing them.
    video.addEventListener("seeking", abandon);
    return () => {
      video.removeEventListener("waiting", start);
      video.removeEventListener("stalled", start);
      video.removeEventListener("playing", end);
      video.removeEventListener("pause", abandon);
      video.removeEventListener("ended", abandon);
      video.removeEventListener("seeking", abandon);
    };
  }, [video, id]);
}
