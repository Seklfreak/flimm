import { getAccessToken, handleUnauthorized } from "./auth";

// ---- Types (docs/api.md) ----------------------------------------------------

export interface AppConfig {
  app_name: string;
  oidc_issuer: string;
  oidc_client_id: string;
  version: string;
}

export type VideoType = "video" | "short" | "stream";

export interface VideoSummary {
  id: string;
  title: string;
  channel: { id: string; name: string; thumb_url: string };
  thumb_url: string;
  duration: number;
  published: string;
  downloaded: string;
  type: VideoType;
  subtitle_langs: string[];
  has_auto_subtitles: boolean;
  watched: boolean;
  /** Taken out of the viewer's feeds without being watched — a decision
   *  separate from `watched` and never written to TubeArchivist. Feeds and
   *  up next never return a dismissed video; channel pages, playlists,
   *  search and history do, so a viewer can find and restore it. */
  dismissed: boolean;
  position: number;
  progress: number;
  last_played_at: string | null;
}

export interface SubtitleTrack {
  lang: string;
  source: "user" | "auto";
  url: string;
}

/**
 * What a player may do with a segment. Only `skip` may be skipped
 * automatically; `mute` is muted for its length, `poi` is a single point of
 * interest (the highlight, where `start === end`) and `full` labels the whole
 * video rather than a range of it. `chapter` segments never reach this list —
 * they come back from `GET /videos/{id}/chapters`.
 */
export type SponsorActionType = "skip" | "mute" | "poi" | "full" | "chapter";

export interface SponsorSegment {
  category: string;
  /** Absent on a server that predates action types, which only sent skips. */
  action_type?: SponsorActionType;
  start: number;
  end: number;
}

export type ChaptersSource = "embedded" | "sponsorblock" | "description" | "none";

/**
 * The list the player steps through. `shuffle` is an opaque seed: the server
 * derives a stable order from it, so every client sharing the seed agrees on
 * what comes next.
 */
export interface PlayContext {
  feed?: string;
  playlist?: string;
  channel?: string;
  shuffle?: string;
  /** "1" when the player should open in audio-only mode; carried through every link so the mode survives next/previous, autoplay and a reload. */
  audio?: string;
}

/** Where a video sits in the list the player is stepping through. */
export interface NavResponse {
  /** -1 when the video isn't in the context list. */
  index: number;
  total: number;
  previous: VideoSummary | null;
  next: VideoSummary | null;
  /** Head of the list — the entry point for a shuffled run. */
  first: VideoSummary | null;
}

export interface Chapter {
  start: number;
  end: number;
  title: string;
}

export interface ChaptersResponse {
  source: ChaptersSource;
  chapters: Chapter[];
}

export interface VideoPlaylistRef {
  id: string;
  name: string;
  position: number;
  count: number;
}

/** One source rendition TA muxed the video from. `codec` is a short name
 *  (`avc1`, `vp09`, `av01`, `mp4a`, `opus`); the player maps it to a MIME
 *  string to ask the browser whether it can decode it — see player/codecGate. */
export interface StreamInfo {
  type: "video" | "audio";
  codec: string;
  width: number;
  height: number;
  bitrate: number;
}

export type HLSState = "pending" | "running" | "done" | "failed";

/** `h264` up to 1080p, `hevc` at 1440 and 2160. An unknown value is left in
 *  rather than dropped: a rung added to the contract later is the browser's to
 *  refuse, not something to hide from the picker. */
export type HLSCodec = "h264" | "hevc" | (string & {});

/** One rung of the compatible-rendition ladder (docs/api.md, tallest first). */
export interface HLSVariant {
  height: number;
  url: string;
  state: HLSState;
  codec: HLSCodec;
  /** Fraction of that rendition that has been transcoded, 0–1. Only a number
   *  to show while preparing — it says nothing about *where* those segments
   *  are, because the playlist is complete from the first request. */
  hls_progress: number;
}

/** What `POST /videos/{id}/hls` answers with. */
export interface HLSStatus {
  state: HLSState;
  height: number;
  hls_progress: number;
}

export interface Video extends Omit<VideoSummary, "channel"> {
  description: string;
  height: number;
  media_url: string;
  audio_url: string;
  /** The same audio as AAC in MP4, for players that cannot decode Opus in
   *  WebM. The web player stays on `audio_url`; optional on older servers. */
  audio_aac_url?: string;
  /** The default compatible rendition. Optional here only because a server
   *  older than the feature has none. */
  hls_url?: string;
  hls_state?: HLSState;
  /** The quality ladder, tallest first. Absent on a server written before it. */
  hls_variants?: HLSVariant[];
  youtube_url: string;
  /** Absent on an older server — which means "unknown", never "unplayable". */
  streams?: StreamInfo[];
  subtitles: SubtitleTrack[];
  sponsorblock: SponsorSegment[];
  stats: { views: number; likes: number };
  tags: string[];
  playlists: VideoPlaylistRef[];
  channel: ChannelSummary;
}

export interface FeedRef {
  id: string;
  name: string;
}

export interface ChannelSummary {
  id: string;
  name: string;
  thumb_url: string;
  banner_url: string;
  video_count: number;
  unseen_count: number;
  last_upload: string | null;
  subscribed: boolean;
  feeds: FeedRef[];
}

export interface Channel extends ChannelSummary {
  description: string;
}

export type FeedSort = "newest" | "oldest" | "shortest" | "longest";

export interface Feed {
  id: string;
  name: string;
  channel_ids: string[];
  channel_count: number;
  unseen_count: number;
  sort: FeedSort;
  hide_seen: boolean;
  include_shorts: boolean;
  subtitles_only: boolean;
  pinned: boolean;
  position: number;
  created_at: string;
  updated_at: string;
}

export const EVERYTHING_ID = "everything";

export interface FeedInput {
  name: string;
  channel_ids: string[];
  sort: FeedSort;
  hide_seen: boolean;
  include_shorts: boolean;
  subtitles_only: boolean;
  pinned: boolean;
}

export interface PlaylistSummary {
  id: string;
  name: string;
  kind: "custom" | "channel";
  channel: { id: string; name: string } | null;
  thumb_url: string;
  video_count: number;
  total_duration: number;
  seen_count: number;
  in_progress_count: number;
  progress: number;
  resume_video_id: string | null;
  pinned: boolean;
  /** A music playlist: audio-only playback, and no watch state is recorded or reported. Seeds `audio=1` on every link into it. */
  music: boolean;
}

export interface Playlist extends PlaylistSummary {
  items: { position: number; video: VideoSummary }[];
}

export interface HistoryEntry {
  id: string;
  video: VideoSummary;
  played_at: string;
  state: "in_progress" | "seen";
}

export interface Page<T> {
  items: T[];
  page: number;
  page_size: number;
  /** Exact only when `has_more` is false — lists are composed lazily. */
  total: number;
  /** Whether another page exists. Page on this, not on `total`. */
  has_more?: boolean;
  /**
   * Resumes exactly here on the next request. Following it makes a deep page
   * cost what the first one did; without it the server re-walks the offset.
   */
  next_cursor?: string;
}

export interface Prefs {
  autoplay: boolean;
  playback_speed: number;
  /** Language code, or "off" when the viewer turned subtitles off. Defaults to "en". */
  subtitle_lang: string;
  subtitle_size: "small" | "medium" | "large";
  skip_sponsors: boolean;
  everything_sort: FeedSort;
  everything_hide_seen: boolean;
  everything_include_shorts: boolean;
  theme: "system" | "light" | "dark";
}

export interface Me {
  id: string;
  name: string;
  email: string;
  is_admin: boolean;
  prefs: Prefs;
}

export interface SubtitleHit {
  lang: string;
  start: number;
  end: number;
  text: string;
}

export type SearchScope = "all" | "titles" | "subtitles" | "channels" | "playlists";

export interface SearchResult {
  took_ms: number;
  videos: { total: number; items: { video: VideoSummary; subtitle_hits: SubtitleHit[] }[] };
  channels: { total: number; items: (ChannelSummary & { match_count: number })[] };
  playlists: { total: number; items: (PlaylistSummary & { match_count: number })[] };
}

export type FeedView = "unseen" | "continue" | "all";
export type ChannelSort = "name" | "videos" | "unseen" | "last_upload";
export type HistoryFilter = "all" | "in_progress" | "seen";

// ---- Client -----------------------------------------------------------------

export class ApiError extends Error {
  status: number;
  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

const BASE = "/api/v1";

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const token = getAccessToken();
  const headers: HeadersInit = {
    ...(init?.headers ?? {}),
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
  };
  const res = await fetch(BASE + path, { ...init, headers });
  if (res.status === 401) {
    handleUnauthorized();
    throw new ApiError(401, "unauthorized");
  }
  if (!res.ok) {
    let msg = res.statusText;
    try {
      const body = await res.json();
      if (body?.error) msg = body.error;
    } catch {
      /* ignore */
    }
    throw new ApiError(res.status, msg);
  }
  if (res.status === 204) return undefined as T;
  return res.json() as Promise<T>;
}

const json = (method: string, body: unknown): RequestInit => ({
  method,
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify(body),
});

function qs<T extends object>(params: T): string {
  const p = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue;
    p.set(k, String(v));
  }
  const s = p.toString();
  return s ? `?${s}` : "";
}

export const PAGE_SIZE = 30;

/**
 * Where in a list to read from: the first page, or wherever the last response
 * said to resume. Lists composed lazily by the server (feed and channel
 * videos) hand back a cursor; following it beats asking for an offset the
 * server would have to walk to.
 */
export interface PageAt {
  page: number;
  cursor?: string;
}

function pageAt(at: PageAt) {
  return at.cursor ? { cursor: at.cursor, page_size: PAGE_SIZE } : { page: at.page, page_size: PAGE_SIZE };
}

export const api = {
  // Unauthenticated; used before the OIDC client exists.
  config: (): Promise<AppConfig> =>
    fetch(`${BASE}/config`).then((r) => {
      if (!r.ok) throw new ApiError(r.status, "config unavailable");
      return r.json();
    }),

  me: () => req<Me>("/me"),
  updatePrefs: (patch: Partial<Prefs>) => req<Prefs>("/me/prefs", json("PATCH", patch)),
  mediaSession: () => req<void>("/session/media", { method: "POST" }),

  feeds: () => req<Feed[]>("/feeds"),
  feed: (id: string) => req<Feed>(`/feeds/${id}`),
  createFeed: (input: FeedInput) => req<Feed>("/feeds", json("POST", input)),
  updateFeed: (id: string, input: FeedInput) => req<Feed>(`/feeds/${id}`, json("PUT", input)),
  deleteFeed: (id: string) => req<void>(`/feeds/${id}`, { method: "DELETE" }),
  reorderFeeds: (ids: string[]) => req<void>("/feeds/reorder", json("POST", { ids })),
  feedVideos: (id: string, view: FeedView | undefined, at: PageAt) =>
    req<Page<VideoSummary>>(`/feeds/${id}/videos${qs({ view, ...pageAt(at) })}`),
  markFeedSeen: (id: string) => req<void>(`/feeds/${id}/mark-seen`, { method: "POST" }),

  channels: (opts: { q?: string; sort?: ChannelSort; unfeeded?: boolean; page: number; page_size?: number }) =>
    req<Page<ChannelSummary>>(
      `/channels${qs({ q: opts.q, sort: opts.sort, unfeeded: opts.unfeeded ? "true" : undefined, page: opts.page, page_size: opts.page_size ?? PAGE_SIZE })}`,
    ),
  channel: (id: string) => req<Channel>(`/channels/${id}`),
  channelVideos: (id: string, view: "all" | "unseen", at: PageAt) =>
    req<Page<VideoSummary>>(`/channels/${id}/videos${qs({ view, ...pageAt(at) })}`),
  channelPlaylists: (id: string) => req<PlaylistSummary[]>(`/channels/${id}/playlists`),
  setChannelFeeds: (id: string, feed_ids: string[]) =>
    req<void>(`/channels/${id}/feeds`, json("PUT", { feed_ids })),
  markChannelSeen: (id: string) => req<void>(`/channels/${id}/mark-seen`, { method: "POST" }),

  video: (id: string) => req<Video>(`/videos/${id}`),
  upNext: (id: string, ctx: PlayContext, page: number) =>
    req<Page<VideoSummary>>(`/videos/${id}/up-next${qs({ ...ctx, page, page_size: PAGE_SIZE })}`),
  nav: (id: string, ctx: PlayContext) => req<NavResponse>(`/videos/${id}/nav${qs(ctx)}`),
  chapters: (id: string) => req<ChaptersResponse>(`/videos/${id}/chapters`),
  // Starts (or re-aims) a compatible rendition without waiting. `height` picks
  // the rung; `from` is the resume position, so the encoder produces the part
  // that is about to be watched first. Idempotent — it is also the progress
  // poll while a rendition is being prepared.
  startHLS: (id: string, height?: number, from?: number) =>
    req<HLSStatus>(`/videos/${id}/hls${qs({ height, from: from ? Math.floor(from) : undefined })}`, {
      method: "POST",
    }),
  // `playlistId` is the play context, not the video's playlist membership —
  // when it names a music playlist the server records no watch state at all
  // (see docs/api.md "Music playlists"). Every heartbeat path must pass it.
  progress: (id: string, position: number, playlistId?: string) =>
    req<{ position: number; watched: boolean }>(
      `/videos/${id}/progress${qs({ playlist: playlistId })}`,
      json("POST", { position }),
    ),
  setWatched: (id: string, watched: boolean) =>
    req<void>(`/videos/${id}/watched`, json("POST", { watched })),
  startOver: (id: string) => req<void>(`/videos/${id}/progress`, { method: "DELETE" }),
  // Idempotent both ways — an undo control can never fail on a double tap.
  dismissVideo: (id: string) => req<{ dismissed: boolean }>(`/videos/${id}/dismiss`, { method: "POST" }),
  undismissVideo: (id: string) => req<{ dismissed: boolean }>(`/videos/${id}/dismiss`, { method: "DELETE" }),

  playlists: (kind: "custom" | "channel" | undefined, page: number) =>
    req<Page<PlaylistSummary>>(`/playlists${qs({ kind, page, page_size: PAGE_SIZE })}`),
  pinnedPlaylists: () => req<PlaylistSummary[]>("/playlists/pinned"),
  setPlaylistPinned: (id: string, pinned: boolean) =>
    req<void>(`/playlists/${id}/pinned`, json("PUT", { pinned })),
  setPlaylistMusic: (id: string, music: boolean) =>
    req<void>(`/playlists/${id}/music`, json("PUT", { music })),
  playlist: (id: string) => req<Playlist>(`/playlists/${id}`),
  createPlaylist: (name: string) => req<PlaylistSummary>("/playlists", json("POST", { name })),
  renamePlaylist: (id: string, name: string) =>
    req<PlaylistSummary>(`/playlists/${id}`, json("PATCH", { name })),
  deletePlaylist: (id: string) => req<void>(`/playlists/${id}`, { method: "DELETE" }),
  playlistAction: (
    id: string,
    video_id: string,
    action: "add" | "remove" | "up" | "down" | "top" | "bottom",
  ) => req<void>(`/playlists/${id}/videos`, json("POST", { video_id, action })),

  history: (filter: HistoryFilter, q: string, page: number) =>
    req<Page<HistoryEntry>>(`/history${qs({ filter, q, page, page_size: PAGE_SIZE })}`),
  deleteHistory: (entryId: string) => req<void>(`/history/${entryId}`, { method: "DELETE" }),

  search: (q: string, opts: { scope?: SearchScope; unseen?: boolean; feed?: string }) =>
    req<SearchResult>(
      `/search${qs({ q, scope: opts.scope, unseen: opts.unseen ? "true" : undefined, feed: opts.feed })}`,
    ),
};

// Progress heartbeat that survives page unload: fetch with keepalive carries
// the Bearer header, which navigator.sendBeacon cannot.
export function sendProgressBeacon(id: string, position: number, playlistId?: string) {
  const token = getAccessToken();
  try {
    void fetch(`${BASE}/videos/${id}/progress${qs({ playlist: playlistId })}`, {
      method: "POST",
      keepalive: true,
      headers: {
        "Content-Type": "application/json",
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
      },
      body: JSON.stringify({ position: Math.floor(position) }),
    });
  } catch {
    /* page is going away */
  }
}
