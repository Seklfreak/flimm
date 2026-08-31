/** What the player does when a video reaches its end.
 *
 *  Autoplay advances only when there is something to advance to; every other
 *  ending stays on the video, where the end card says so rather than leaving
 *  the last frame standing as if playback were merely paused. Both halves of
 *  that rule — the page that navigates and the player that raises the card —
 *  ask this so they cannot disagree, and `PlaybackEnd.decide` in FlimmKit is
 *  the same rule for the Apple clients.
 */
export type PlaybackEnd = "advance" | "finished";

export function playbackEnd(autoplay: boolean, hasNext: boolean): PlaybackEnd {
  return autoplay && hasNext ? "advance" : "finished";
}
