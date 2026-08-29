import { useEffect } from "react";
import type { SponsorSegment } from "@/lib/api";
import { segmentToMute, segmentToSkip, type SponsorActions } from "./chapterMath";

// Applies SponsorBlock segments while playing (prefs.skip_sponsors is the
// master switch): a `skip` segment in a category set to "skip" is seeked past,
// a `mute` one is muted for its length and the video keeps playing. A category
// set to "ask" is *not* acted on here — the player offers a button for it —
// and one set to "off" does nothing at all. Which is which is decided in
// chapterMath, shared with the scrubber and mirrored in FlimmKit.
export function useSponsorSkip(
  video: HTMLVideoElement | null,
  segments: SponsorSegment[],
  enabled: boolean,
  actions: SponsorActions,
  onSkip?: (s: SponsorSegment) => void,
) {
  useEffect(() => {
    if (!video || !enabled || segments.length === 0) return;
    // Whether *we* muted, and what the viewer had it on before we did, so
    // unmuting at the end of a segment restores their setting rather than
    // forcing audio on. A viewer who unmutes mid-segment keeps their choice:
    // the segment is already ours, so nothing re-mutes it.
    let muting = false;
    let wasMuted = false;
    const restore = () => {
      if (!muting) return;
      video.muted = wasMuted;
      muting = false;
    };
    const onTime = () => {
      const t = video.currentTime;
      const skip = segmentToSkip(segments, t, actions);
      if (skip) {
        restore();
        video.currentTime = skip.end;
        onSkip?.(skip);
        return;
      }
      if (segmentToMute(segments, t, actions)) {
        if (!muting) {
          wasMuted = video.muted;
          video.muted = true;
          muting = true;
        }
        return;
      }
      restore();
    };
    video.addEventListener("timeupdate", onTime);
    return () => {
      video.removeEventListener("timeupdate", onTime);
      restore();
    };
  }, [video, segments, enabled, actions, onSkip]);
}
