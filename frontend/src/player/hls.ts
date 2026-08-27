import type HlsType from "hls.js";

/**
 * hls.js, loaded only when a compatible rendition is actually going to play.
 *
 * The archive-direct path is the common one and must not pay for this: the
 * import is dynamic, so Vite emits hls.js as its own chunk that the main
 * bundle never references. Safari plays HLS natively and never loads it at
 * all (see `supportsNativeHLS`).
 */
let pending: Promise<typeof HlsType> | null = null;

export function loadHls(): Promise<typeof HlsType> {
  pending ??= import("hls.js").then((m) => m.default);
  return pending;
}

/**
 * Config shared by every instance.
 *
 * `xhrSetup` sets `withCredentials` because `/media/*` is authorised by the
 * `flimm_media` cookie — `<video>` cannot send a Bearer header, so the whole
 * media route rides on that cookie, and hls.js must send it on the playlist,
 * the init segment and every media segment. Same-origin XHR sends cookies
 * anyway; this is what keeps it working if media is ever served from another
 * host.
 *
 * The timeouts are long on purpose: a segment the encoder has not reached
 * **blocks** server-side for up to `MEDIA_SEGMENT_WAIT` (60 s) rather than
 * 404ing, and hls.js's 20 s default would give up on a request that was going
 * to succeed.
 */
export const HLS_CONFIG = {
  xhrSetup: (xhr: XMLHttpRequest) => {
    xhr.withCredentials = true;
  },
  manifestLoadingTimeOut: 30_000,
  fragLoadingTimeOut: 90_000,
  fragLoadingMaxRetry: 6,
  levelLoadingTimeOut: 30_000,
} as const;
