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
  position: number;
  progress: number;
  last_played_at: string | null;
}

export interface SubtitleTrack {
  lang: string;
  source: "user" | "auto";
  url: string;
}

export interface SponsorSegment {
  category: string;
  start: number;
  end: number;
}

export type ChaptersSource = "embedded" | "description" | "none";

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

export interface Video extends Omit<VideoSummary, "channel"> {
  description: string;
  height: number;
  media_url: string;
  youtube_url: string;
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
  total: number;
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
  feedVideos: (id: string, view: FeedView | undefined, page: number) =>
    req<Page<VideoSummary>>(`/feeds/${id}/videos${qs({ view, page, page_size: PAGE_SIZE })}`),
  markFeedSeen: (id: string) => req<void>(`/feeds/${id}/mark-seen`, { method: "POST" }),

  channels: (opts: { q?: string; sort?: ChannelSort; unfeeded?: boolean; page: number; page_size?: number }) =>
    req<Page<ChannelSummary>>(
      `/channels${qs({ q: opts.q, sort: opts.sort, unfeeded: opts.unfeeded ? "true" : undefined, page: opts.page, page_size: opts.page_size ?? PAGE_SIZE })}`,
    ),
  channel: (id: string) => req<Channel>(`/channels/${id}`),
  channelVideos: (id: string, view: "all" | "unseen", page: number) =>
    req<Page<VideoSummary>>(`/channels/${id}/videos${qs({ view, page, page_size: PAGE_SIZE })}`),
  channelPlaylists: (id: string) => req<PlaylistSummary[]>(`/channels/${id}/playlists`),
  setChannelFeeds: (id: string, feed_ids: string[]) =>
    req<void>(`/channels/${id}/feeds`, json("PUT", { feed_ids })),
  markChannelSeen: (id: string) => req<void>(`/channels/${id}/mark-seen`, { method: "POST" }),

  video: (id: string) => req<Video>(`/videos/${id}`),
  upNext: (id: string, ctx: PlayContext) => req<VideoSummary[]>(`/videos/${id}/up-next${qs(ctx)}`),
  nav: (id: string, ctx: PlayContext) => req<NavResponse>(`/videos/${id}/nav${qs(ctx)}`),
  chapters: (id: string) => req<ChaptersResponse>(`/videos/${id}/chapters`),
  progress: (id: string, position: number) =>
    req<{ position: number; watched: boolean }>(`/videos/${id}/progress`, json("POST", { position })),
  setWatched: (id: string, watched: boolean) =>
    req<void>(`/videos/${id}/watched`, json("POST", { watched })),
  startOver: (id: string) => req<void>(`/videos/${id}/progress`, { method: "DELETE" }),

  playlists: (kind: "custom" | "channel" | undefined, page: number) =>
    req<Page<PlaylistSummary>>(`/playlists${qs({ kind, page, page_size: PAGE_SIZE })}`),
  pinnedPlaylists: () => req<PlaylistSummary[]>("/playlists/pinned"),
  setPlaylistPinned: (id: string, pinned: boolean) =>
    req<void>(`/playlists/${id}/pinned`, json("PUT", { pinned })),
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
export function sendProgressBeacon(id: string, position: number) {
  const token = getAccessToken();
  try {
    void fetch(`${BASE}/videos/${id}/progress`, {
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
