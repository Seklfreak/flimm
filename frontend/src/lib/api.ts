import { getAccessToken, handleUnauthorized } from "./auth";

// ---- Types (docs/api.md) ----------------------------------------------------

export interface AppConfig {
  app_name: string;
  oidc_issuer: string;
  oidc_client_id: string;
  version: string;
  /** The deployment runs with `ANALYTICS_DISABLED=true`: clients report
   *  nothing, whatever analytics endpoint they were built with. */
  analytics_disabled?: boolean;
  /** The server has an APNs key: a feed's `notify` reaches an iPhone or
   *  iPad. Without it the editor does not offer the switch. */
  push_enabled?: boolean;
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
/** A page of up-next videos that says whether it is the queue or a guess.
 *  `suggestions` is set only once the context has run out; the items are then
 *  similar videos, which no client may autoplay into or show under the
 *  context's own name. */
export interface UpNextPage extends Page<VideoSummary> {
  suggestions?: boolean;
}

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

/** One archived comment. Normalised by the server, so no client parses
 *  TubeArchivist's own key names.
 *
 *  There is deliberately no author avatar: the archive holds a Google CDN URL
 *  for it, and loading that would announce every video its viewer opens to a
 *  third party — the one thing archived comments otherwise avoid. */
export interface Comment {
  id: string;
  author: string;
  author_id: string;
  text: string;
  likes: number;
  /** RFC 3339, or null on an archive that kept only `time_text`. */
  published: string | null;
  /** Upstream's own wording ("2 days ago"); the fallback for `published`. */
  time_text: string;
  hearted: boolean;
  from_uploader: boolean;
  replies: Comment[];
}

/** `GET /videos/{id}/loudness` — how loud a video was measured to be, and the
 *  gain a player applies to it. `gain_db` is never positive: no client can
 *  amplify uniformly, so normalisation only ever turns a video down. Only
 *  `state: "done"` carries numbers. */
export interface Loudness {
  state: "pending" | "running" | "done" | "failed";
  /** 0–1 through the file while the pass runs. Optional only because a server
   *  older than the field does not send it. */
  progress?: number;
  gain_db: number;
  target_lufs: number;
  measured_lufs: number;
  peak_dbtp: number;
  range_lu: number;
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

/** `GET /prepare` — the background job that derives scrub previews and loudness
 *  for what is near the top of the feeds, ahead of anyone opening it. */
export interface PrepareStatus {
  /** `paused` is running-but-waiting: something is being played, and the job
   *  stays out of its way. */
  state: "idle" | "running" | "paused";
  done: number;
  /** 0 when no pass is in flight. */
  total: number;
  /** The title being prepared, when there is one. */
  video: string;
  /** When the last pass finished; absent before the first one. */
  prepared_at?: string;
}

export type HLSState = "pending" | "running" | "done" | "failed";

/** `h264` up to 1080p, `hevc` at 1440 and 2160. An unknown value is left in
 *  rather than dropped: a rung added to the contract later is the browser's to
 *  refuse, not something to hide from the picker. */
export type HLSCodec = "h264" | "hevc" | (string & {});

/** `GET /admin/sessions` — every playback happening on the server right now,
 *  whoever it belongs to. Admin only; see docs/api.md, "Live sessions". */
export interface LiveResponse {
  sessions: LiveSession[];
  /** Every running transcode, including ones no session is attached to. */
  jobs: LiveJob[];
  /** The same recent stalls `/healthz` shows an admin. */
  stalls: LiveStall[];
  /** The server's clock, so ages are computed against it rather than the
   *  browser's — the two are not the same and the difference is unbounded. */
  now: string;
}

export interface LiveSession {
  user_id: string;
  /** An email or a display name, whichever the account has. */
  user?: string;
  video_id: string;
  title?: string;
  channel_name?: string;
  /** Derived from the User-Agent unless a player named itself. `apple` is a
   *  native client that did not say which one. */
  client?: "web" | "ios" | "ipados" | "tvos" | "apple" | (string & {});
  /** The screen's own name — only a player that publishes a session has one. */
  device?: string;
  position: number;
  duration: number;
  paused: boolean;
  /** When the session was first *seen*, which is not when playback started. */
  started_at: string;
  updated_at: string;
  /** A media request is open right now. Not the same as "playing": a browser
   *  that has buffered the whole file asks for nothing for minutes. */
  streaming: boolean;
  /** What has left the machine for this session, across every request in it. */
  bytes: number;
  stalls: number;
  last_stall?: string;
  delivery: LiveDelivery;
  /** The player's own readings, for a client that publishes them (tvOS). */
  stats?: LivePlaybackStats;
}

export interface LiveDelivery {
  /** Absent until something has been streamed: a heartbeat can arrive first. */
  kind?: "direct" | "rendition" | "audio";
  height?: number;
  /** The transcode behind a rendition, or absent when it is finished — which
   *  is itself the answer to "why is this stalling". */
  job?: LiveJob;
}

export interface LiveJob {
  video_id: string;
  height: number;
  segments: number;
  /** The fraction of the rendition that exists — not where anyone is watching. */
  progress: number;
  /** The segment ffmpeg is on, or -1 when the job is waiting for the slot. */
  encoder_segment: number;
}

export interface LiveStall {
  at: string;
  video_id: string;
  position: number;
  seconds: number;
  height: number;
  client: string;
  /** The server's attribution: `encoder_behind`, `delivery`, `source`, `unknown`. */
  reason: string;
  segment: number;
  encoder: number;
}

/** What a player published about itself, relayed untouched. The full shape is
 *  in docs/api.md under "Playback stats"; this is what the admin view reads. */
export interface LivePlaybackStats {
  delivery: {
    kind: string;
    reason: string;
    source_height: number;
    source_codec: string;
    rendition?: { height: number; codec: string; state: string; progress: number; preparing: boolean };
  };
  player: {
    status: string;
    likely_to_keep_up: boolean;
    picture_width: number;
    picture_height: number;
    buffer_ahead?: number;
    dropped_frames?: number;
    observed_bitrate?: number;
  };
  device: { decoders: string[]; screen_height: number };
}

export type StatsRange = "all" | "year" | "month";

/** `GET /stats` — see docs/api.md, "Watch stats", for what these can honestly say. */
export interface WatchStats {
  started: number;
  finished: number;
  /** The summed furthest point reached in each video: a floor, not a stopwatch. */
  seconds: number;
  since: string | null;
  range: StatsRange;
  zone: string;
  top_channels: { id: string; name: string; videos: number; seconds: number }[];
  /** 24 counts, midnight first. */
  by_hour: number[];
  /** 7 counts, Monday first. */
  by_weekday: number[];
  by_month: { month: string; videos: number; seconds: number }[];
}

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
  /** The WebVTT track of scrub-preview stills. Optional only because a server
   *  older than the feature has none; a 404 means "not derived yet". */
  preview_url?: string;
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
  // `dislikes` is absent unless the deployment enabled Return YouTube Dislike
  // *and* the service knows this video — "nobody knows" rather than "none".
  stats: { views: number; likes: number; dislikes?: number };
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
  /** Pinned to the sidebar — per-user state, like a playlist pin. */
  pinned: boolean;
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
  /** Playlist sources: single series next to whole channels; the feed is the union. */
  playlist_ids: string[];
  playlist_count: number;
  /** Channels watched for *new* series — announced via GET /feeds/{id}/new-series. */
  series_watch_channel_ids: string[];
  unseen_count: number;
  sort: FeedSort;
  hide_seen: boolean;
  include_shorts: boolean;
  subtitles_only: boolean;
  pinned: boolean;
  /** New downloads for this feed are pushed to the account's iPhones and
   *  iPads (see `push_enabled` on the config and `push_devices` on Me). */
  notify: boolean;
  position: number;
  created_at: string;
  updated_at: string;
}

export const EVERYTHING_ID = "everything";

export interface FeedInput {
  name: string;
  channel_ids: string[];
  playlist_ids: string[];
  series_watch_channel_ids: string[];
  sort: FeedSort;
  hide_seen: boolean;
  include_shorts: boolean;
  subtitles_only: boolean;
  pinned: boolean;
  notify: boolean;
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
  /** Feeds holding this playlist as a source — the "In feeds:" badge. */
  feeds: FeedRef[];
}

export interface Playlist extends PlaylistSummary {
  items: { position: number; video: VideoSummary }[];
}

export interface HistoryEntry {
  id: string;
  video: VideoSummary;
  played_at: string;
  state: "in_progress" | "seen";
  /** The series the video belongs to through a feed's playlist source — the
   *  resume context when set (up next = the next episode). */
  playlist_id: string | null;
  /** The feed holding the video's channel — the resume context when no
   *  series claims it. Null when no feed holds the video at all. */
  feed: FeedRef | null;
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
  /** The master switch: off and no SponsorBlock segment acts at all. */
  skip_sponsors: boolean;
  /** What each category does while `skip_sponsors` is on: "skip", "ask" (a
   *  button in the player) or "off". The server sends every category it
   *  knows, so a missing one means a category this client predates. */
  sponsor_actions: Record<string, string>;
  /** Crowd-sourced titles: "off", "manual" (submissions only) or "all"
   *  (submissions, and a tidied original where there are none). */
  dearrow_titles: DeArrowSetting;
  /** Crowd-sourced thumbnails, set independently of titles. */
  dearrow_thumbnails: DeArrowSetting;
  /** Even out the difference between channels: the player applies the gain
   *  from `GET /videos/{id}/loudness` instead of playing every video at
   *  whatever level it was uploaded at. */
  normalize_loudness: boolean;
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
  /** iPhones and iPads registered for feed notifications. Zero means a
   *  feed's `notify` reaches nobody until the iPhone app has signed in. */
  push_devices: number;
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

export type FeedView = "unseen" | "all";

export type DeArrowSetting = "off" | "manual" | "all";
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
  newSeries: (id: string) => req<PlaylistSummary[]>(`/feeds/${id}/new-series`),
  dismissNewSeries: (id: string, playlistId: string) =>
    req<void>(`/feeds/${id}/new-series/${playlistId}/dismiss`, { method: "POST" }),

  channels: (opts: { q?: string; sort?: ChannelSort; unfeeded?: boolean; page: number; page_size?: number }) =>
    req<Page<ChannelSummary>>(
      `/channels${qs({ q: opts.q, sort: opts.sort, unfeeded: opts.unfeeded ? "true" : undefined, page: opts.page, page_size: opts.page_size ?? PAGE_SIZE })}`,
    ),
  channel: (id: string) => req<Channel>(`/channels/${id}`),
  channelVideos: (id: string, view: "all" | "unseen", at: PageAt) =>
    req<Page<VideoSummary>>(`/channels/${id}/videos${qs({ view, ...pageAt(at) })}`),
  channelPlaylists: (id: string) => req<PlaylistSummary[]>(`/channels/${id}/playlists`),
  pinnedChannels: () => req<ChannelSummary[]>("/channels/pinned"),
  setChannelPinned: (id: string, pinned: boolean) =>
    req<void>(`/channels/${id}/pinned`, json("PUT", { pinned })),
  setChannelFeeds: (id: string, feed_ids: string[]) =>
    req<void>(`/channels/${id}/feeds`, json("PUT", { feed_ids })),
  markChannelSeen: (id: string) => req<void>(`/channels/${id}/mark-seen`, { method: "POST" }),
  /** Admin only: asks TubeArchivist to index the channel's playlists (the prerequisite for series feed sources). The discovery runs as a TA task. */
  indexChannelPlaylists: (id: string) => req<void>(`/channels/${id}/index-playlists`, { method: "POST" }),
  /** Admin only: flips TubeArchivist's own subscription — whether the archive keeps downloading the channel's new videos. */
  setChannelSubscribed: (id: string, subscribed: boolean) =>
    req<void>(`/channels/${id}/subscribed`, json("PUT", { subscribed })),
  /** Admin only: subscribe a channel the archive may not know yet — URL, @handle or UC… id; TA resolves it in a background task. */
  subscribeNewChannel: (channel: string) => req<void>("/channels", json("POST", { channel })),

  video: (id: string) => req<Video>(`/videos/${id}`),
  upNext: (id: string, ctx: PlayContext, page: number, before?: boolean) =>
    req<UpNextPage>(`/videos/${id}/up-next${qs({ ...ctx, before: before ? "true" : undefined, page, page_size: PAGE_SIZE })}`),
  nav: (id: string, ctx: PlayContext) => req<NavResponse>(`/videos/${id}/nav${qs(ctx)}`),
  chapters: (id: string) => req<ChaptersResponse>(`/videos/${id}/chapters`),
  // Asking is what starts the analysis pass; the answer turns up on a later
  // call, exactly like a rendition's state.
  loudness: (id: string) => req<Loudness>(`/videos/${id}/loudness`),
  prepareStatus: () => req<PrepareStatus>("/prepare"),
  comments: (id: string, page: number) =>
    req<Page<Comment>>(`/videos/${id}/comments${qs({ page, page_size: PAGE_SIZE })}`),
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
  // A mid-playback stall the viewer saw. The client knows it happened; the
  // server knows why (see useStallReport and docs/api.md). Fire and forget.
  reportStall: (id: string, body: { position: number; seconds: number; height: number; client: string }) =>
    req<void>(`/videos/${id}/stall`, json("POST", body)),
  // What a viewer's history adds up to. `tz` decides which evening an 11pm
  // play belongs to, so the browser's own zone is sent (docs/api.md).
  stats: (range: StatsRange, tz: string) =>
    req<WatchStats>(`/stats${qs({ range: range === "all" ? undefined : range, tz })}`),
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
  setPlaylistFeeds: (id: string, feed_ids: string[]) =>
    req<void>(`/playlists/${id}/feeds`, json("PUT", { feed_ids })),
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

  /** Admin only: what the server is doing right now — every account's
   *  playback, what is being transcoded for it, and what recently stalled. */
  liveSessions: () => req<LiveResponse>("/admin/sessions"),

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
