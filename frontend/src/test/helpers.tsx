import type { ReactNode } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { render } from "@testing-library/react";
import { vi } from "vitest";
import type { Feed, PlaylistSummary, Video, VideoSummary } from "@/lib/api";

export function renderWithProviders(ui: ReactNode, { route = "/" }: { route?: string } = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[route]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
}

// Route-table fetch mock: maps "METHOD /api/v1/path" prefixes to JSON bodies.
export function mockFetch(routes: Record<string, unknown | ((url: string, init?: RequestInit) => unknown)>) {
  const calls: { url: string; init?: RequestInit }[] = [];
  const fn = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const url = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
    const method = (init?.method ?? "GET").toUpperCase();
    calls.push({ url, init });
    const path = url.split("?")[0];
    const key = Object.keys(routes).find((k) => {
      const [m, p] = k.split(" ");
      return m === method && path === p;
    });
    if (!key) return new Response(JSON.stringify({ error: `no mock for ${method} ${path}` }), { status: 404 });
    const body = routes[key];
    const data = typeof body === "function" ? (body as (u: string, i?: RequestInit) => unknown)(url, init) : body;
    if (data === undefined) return new Response(null, { status: 204 });
    return new Response(JSON.stringify(data), { status: 200, headers: { "Content-Type": "application/json" } });
  });
  vi.stubGlobal("fetch", fn);
  return { fn, calls };
}

export function video(over: Partial<VideoSummary> = {}): VideoSummary {
  return {
    id: "vid1",
    title: "The Beauty of Bézier Curves",
    channel: { id: "UC1", name: "Freya Holmér", thumb_url: "/media/thumb/channel/UC1" },
    thumb_url: "/media/thumb/video/vid1",
    duration: 1476,
    published: new Date(Date.now() - 3 * 86_400_000).toISOString(),
    downloaded: new Date().toISOString(),
    type: "video",
    subtitle_langs: ["en"],
    has_auto_subtitles: false,
    watched: false,
    position: 561,
    progress: 0.38,
    last_played_at: new Date().toISOString(),
    ...over,
  };
}

export function playlist(over: Partial<PlaylistSummary> = {}): PlaylistSummary {
  return {
    id: "p1",
    name: "Shader Deep Dives",
    kind: "custom",
    channel: null,
    thumb_url: "/media/thumb/playlist/p1",
    video_count: 14,
    total_duration: 15120,
    seen_count: 11,
    in_progress_count: 1,
    progress: 0.78,
    resume_video_id: null,
    pinned: false,
    music: false,
    ...over,
  };
}

export function videoDetail(over: Partial<Video> = {}): Video {
  const { channel: summaryChannel, ...summary } = video();
  return {
    ...summary,
    description: "A description.",
    height: 1080,
    media_url: "/media/video/vid1.mp4",
    audio_url: "/media/audio/vid1.webm",
    youtube_url: "https://www.youtube.com/watch?v=vid1",
    subtitles: [],
    sponsorblock: [],
    stats: { views: 0, likes: 0 },
    tags: [],
    playlists: [],
    channel: {
      ...summaryChannel,
      banner_url: "/media/thumb/channel/UC1/banner",
      video_count: 212,
      unseen_count: 3,
      last_upload: new Date().toISOString(),
      subscribed: true,
      feeds: [],
    },
    ...over,
  };
}

export function feed(over: Partial<Feed> = {}): Feed {
  return {
    id: "f1",
    name: "Home",
    channel_ids: ["UC1"],
    channel_count: 6,
    unseen_count: 7,
    sort: "newest",
    hide_seen: true,
    include_shorts: false,
    subtitles_only: false,
    pinned: true,
    position: 0,
    created_at: "",
    updated_at: "",
    ...over,
  };
}
