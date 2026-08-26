import { useEffect } from "react";
import type { SponsorSegment } from "@/lib/api";

// Auto-skips SponsorBlock segments while playing (prefs.skip_sponsors).
// Only "sponsor"/"selfpromo"/"interaction" categories are skipped; others
// (intro, outro, music_offtopic…) are left alone.
const SKIP = new Set(["sponsor", "selfpromo", "interaction"]);

export function useSponsorSkip(video: HTMLVideoElement | null, segments: SponsorSegment[], enabled: boolean, onSkip?: (s: SponsorSegment) => void) {
  useEffect(() => {
    if (!video || !enabled || segments.length === 0) return;
    const list = segments.filter((s) => SKIP.has(s.category) && s.end > s.start);
    if (list.length === 0) return;
    const onTime = () => {
      const t = video.currentTime;
      for (const s of list) {
        // Skip when inside a segment (with a small margin so a seek to just
        // before the start still triggers).
        if (t >= s.start && t < s.end - 0.5) {
          video.currentTime = s.end;
          onSkip?.(s);
          break;
        }
      }
    };
    video.addEventListener("timeupdate", onTime);
    return () => video.removeEventListener("timeupdate", onTime);
  }, [video, segments, enabled, onSkip]);
}
