import { api } from "./api";

// <video>/<img> cannot send the Bearer header, so media auth rides on the
// flimm_media cookie set by POST /session/media. We call it once after
// login and again (deduplicated) when a media request 401s.
let inflight: Promise<void> | null = null;
let lastRefresh = 0;

export function refreshMediaSession(): Promise<void> {
  if (inflight) return inflight;
  inflight = api
    .mediaSession()
    .then(() => {
      lastRefresh = Date.now();
    })
    .catch(() => {
      /* the caller retries the media load; a second failure is surfaced there */
    })
    .finally(() => {
      inflight = null;
    });
  return inflight;
}

// Retry helper for media elements: refreshes the cookie once and returns a
// cache-busted URL to reassign to src, or null if we already retried recently.
const retried = new Map<string, number>();
export async function retryMediaUrl(url: string): Promise<string | null> {
  const last = retried.get(url) ?? 0;
  if (Date.now() - last < 30_000) return null;
  retried.set(url, Date.now());
  const before = lastRefresh;
  await refreshMediaSession();
  if (lastRefresh === before) return null; // refresh failed; don't loop
  const sep = url.includes("?") ? "&" : "?";
  return `${url}${sep}r=${Date.now()}`;
}
