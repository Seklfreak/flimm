// First-party analytics against a self-hosted Umami instance. The endpoint and
// website id are baked into the bundle at image build time (VITE_UMAMI_*),
// exactly like the Sentry DSN — an image built without them never loads the
// tracker at all. A deployment that has them can still turn reporting off at
// runtime with ANALYTICS_DISABLED=true, which reaches us as
// `analytics_disabled` on /api/v1/config.
//
// Deliberately incurious. Umami's own auto-tracking would report
// `location.pathname` and `document.title`, which here means the id and the
// title of whatever you are watching. So auto-tracking is off and every
// payload is built from the route *pattern* this module holds — `/watch/:id`,
// never `/watch/dQw4w9WgXcQ` — and no event carries a video, channel,
// playlist or query string.

const ENDPOINT = import.meta.env.VITE_UMAMI_URL?.replace(/\/+$/, "") ?? "";
const WEBSITE_ID = import.meta.env.VITE_UMAMI_WEBSITE_ID ?? "";

/** What Umami's tracker exposes once `s.js` has run. */
type Umami = {
  track: (payload: (props: Record<string, unknown>) => Record<string, unknown>) => void;
};

declare global {
  interface Window {
    umami?: Umami;
  }
}

/** A payload builder, queued until the tracker script is there to run it. */
type Build = (props: Record<string, unknown>) => Record<string, unknown>;

let started = false;
let queue: Build[] = [];
/** The route pattern and title every payload — pageview or event — carries. */
let currentUrl = "/";
let currentTitle = "Flimm";

// A tracker that never loads (offline, blocked, wrong URL) must not grow this
// without bound; the oldest calls are the least interesting.
const QUEUE_LIMIT = 20;

/**
 * Load the tracker. Safe to call repeatedly: only the first call with
 * analytics enabled and both build-time values present does anything.
 */
export function startAnalytics(enabled = true): void {
  if (started || !enabled || !ENDPOINT || !WEBSITE_ID) return;
  if (typeof document === "undefined") return;
  started = true;

  const script = document.createElement("script");
  script.defer = true;
  // The tracker is served as /s.js rather than the default /script.js so
  // filter lists don't take it out; the host it posts to is the same origin
  // it was loaded from, said explicitly so a CDN in front can't change it.
  script.src = `${ENDPOINT}/s.js`;
  script.setAttribute("data-website-id", WEBSITE_ID);
  script.setAttribute("data-host-url", ENDPOINT);
  // See the file comment: we build every payload ourselves.
  script.setAttribute("data-auto-track", "false");
  script.addEventListener("load", flush);
  script.addEventListener("error", () => {
    // Blocked or unreachable. Nothing to retry against, so stop collecting.
    queue = [];
  });
  document.head.appendChild(script);
}

/** A pageview. Also fixes the route subsequent events are reported against. */
export function trackScreen(url: string, title: string): void {
  currentUrl = url;
  currentTitle = title;
  send((props) => ({ ...props, url, title }));
}

/**
 * An action on the current screen. Keep `data` free of anything identifying a
 * video, channel, playlist or search term: counts and outcomes only.
 */
export function trackEvent(name: string, data?: Record<string, string>): void {
  // Read now, not when the payload is built: a queued event belongs to the
  // screen it happened on, not to wherever the app had got to by the time the
  // tracker turned up.
  const url = currentUrl;
  const title = currentTitle;
  send((props) => ({
    ...props,
    url,
    title,
    name,
    ...(data && Object.keys(data).length > 0 ? { data } : {}),
  }));
}

// Queues rather than checking whether analytics started: React runs a child's
// effects before its parent's, so the first route is reported before
// ConfigLoader has had the chance to load the tracker. An app that never
// starts one simply drops what little it collected.
function send(build: Build): void {
  if (window.umami) {
    // The backlog goes first, so a call that arrives between the script
    // loading and its load handler running cannot jump the queue.
    if (queue.length > 0) flush();
    window.umami.track(build);
    return;
  }
  queue.push(build);
  if (queue.length > QUEUE_LIMIT) queue = queue.slice(-QUEUE_LIMIT);
}

function flush(): void {
  const pending = queue;
  queue = [];
  for (const build of pending) window.umami?.track(build);
}

/**
 * Every route the app can settle on, as the pattern reported for it. Ordered:
 * `/feeds/:id/edit` has to be tested before `/feeds/:id`.
 *
 * Unknown paths return null rather than a catch-all bucket — the router sends
 * them to `/` anyway, and that navigation is the one worth counting.
 */
const ROUTES: { match: RegExp; url: string; title: string }[] = [
  { match: /^\/$/, url: "/", title: "Home" },
  { match: /^\/feeds\/new$/, url: "/feeds/new", title: "New feed" },
  { match: /^\/feeds\/[^/]+\/edit$/, url: "/feeds/:id/edit", title: "Edit feed" },
  { match: /^\/feeds\/[^/]+$/, url: "/feeds/:id", title: "Feed" },
  { match: /^\/channels$/, url: "/channels", title: "Channels" },
  { match: /^\/channels\/[^/]+$/, url: "/channels/:id", title: "Channel" },
  { match: /^\/playlists$/, url: "/playlists", title: "Playlists" },
  { match: /^\/playlists\/[^/]+$/, url: "/playlists/:id", title: "Playlist" },
  { match: /^\/history$/, url: "/history", title: "History" },
  { match: /^\/search$/, url: "/search", title: "Search" },
  { match: /^\/settings$/, url: "/settings", title: "Settings" },
  { match: /^\/watch\/[^/]+$/, url: "/watch/:id", title: "Watch" },
];

export function routePattern(pathname: string): { url: string; title: string } | null {
  const path = pathname.length > 1 ? pathname.replace(/\/+$/, "") : pathname;
  const route = ROUTES.find((r) => r.match.test(path));
  return route ? { url: route.url, title: route.title } : null;
}

// ---- The shared event vocabulary (mirrored by FlimmKit on Apple) -----------

/** Playback actually started, once per video. */
export function trackPlay(kind: string, audioOnly: boolean): void {
  trackEvent("play", { kind, audio: audioOnly ? "yes" : "no" });
}

/** A search was committed. The scope only — never the query. */
export function trackSearch(scope: string): void {
  trackEvent("search", { scope });
}

/** A feed was created (not every save: edits are not this event). */
export function trackFeedCreated(): void {
  trackEvent("feed-created");
}

/** Exposed for tests, which need each case to start from nothing. */
export function resetAnalyticsForTests(): void {
  started = false;
  queue = [];
  currentUrl = "/";
  currentTitle = "Flimm";
  delete window.umami;
}
