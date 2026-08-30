import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type QueryClient,
} from "@tanstack/react-query";
import { api, type Comment, type Feed, type FeedInput, type Page, type PageAt, type Prefs, type VideoSummary, EVERYTHING_ID } from "./api";

export const keys = {
  me: ["me"] as const,
  feeds: ["feeds"] as const,
  feed: (id: string) => ["feeds", id] as const,
  feedVideos: (id: string, view: string | undefined) => ["feeds", id, "videos", view ?? "default"] as const,
  channels: (q: string, sort: string, unfeeded: boolean) => ["channels", { q, sort, unfeeded }] as const,
  channel: (id: string) => ["channels", id] as const,
  channelVideos: (id: string, view: string) => ["channels", id, "videos", view] as const,
  channelPlaylists: (id: string) => ["channels", id, "playlists"] as const,
  video: (id: string) => ["videos", id] as const,
  upNext: (id: string, ctx: Record<string, string | undefined>) => ["videos", id, "up-next", ctx] as const,
  nav: (id: string, ctx: Record<string, string | undefined>) => ["videos", id, "nav", ctx] as const,
  chapters: (id: string) => ["videos", id, "chapters"] as const,
  comments: (id: string) => ["videos", id, "comments"] as const,
  playlists: (kind: string | undefined) => ["playlists", kind ?? "all"] as const,
  playlist: (id: string) => ["playlists", id] as const,
  pinnedPlaylists: ["playlists", "pinned"] as const,
  history: (filter: string, q: string) => ["history", { filter, q }] as const,
  inProgress: ["history", "in-progress-sidebar"] as const,
  search: (q: string, scope: string, unseen: boolean, feed: string | undefined) =>
    ["search", { q, scope, unseen, feed }] as const,
};

// Generic paged → infinite adapter for the { items, page, page_size, total }
// shape.
//
// `has_more` is the authority: the server composes lists lazily, stopping one
// item past the window it was asked for, so `total` is only a floor while more
// remains. Comparing an offset against it would end the list at the first
// page. `total` stays the fallback for a response without the field.
export function pageParams<T>() {
  return {
    initialPageParam: 0,
    getNextPageParam: (last: Page<T>) => {
      if (last.has_more !== undefined) return last.has_more ? last.page + 1 : undefined;
      const seen = (last.page + 1) * last.page_size;
      return seen < last.total ? last.page + 1 : undefined;
    },
  };
}

// Cursor adapter for the lazily composed video lists. The server hands back a
// `next_cursor` that resumes exactly where the last page stopped; following it
// keeps a deep page as cheap as the first, where asking for `page=40` makes
// the server walk the forty pages before it.
export function cursorParams<T>() {
  return {
    initialPageParam: { page: 0 } as PageAt,
    getNextPageParam: (last: Page<T>, _pages: Page<T>[], lastParam: PageAt): PageAt | undefined => {
      const more = last.has_more ?? (last.page + 1) * last.page_size < last.total;
      if (!more) return undefined;
      return { page: lastParam.page + 1, cursor: last.next_cursor };
    },
  };
}

export function useMe() {
  return useQuery({ queryKey: keys.me, queryFn: api.me, staleTime: 5 * 60_000 });
}

export function usePrefs(): Prefs | undefined {
  return useMe().data?.prefs;
}

export function useUpdatePrefs() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (patch: Partial<Prefs>) => api.updatePrefs(patch),
    onMutate: async (patch) => {
      await qc.cancelQueries({ queryKey: keys.me });
      qc.setQueryData(keys.me, (old: { prefs: Prefs } | undefined) =>
        old ? { ...old, prefs: { ...old.prefs, ...patch } } : old,
      );
    },
    onSettled: () => qc.invalidateQueries({ queryKey: keys.me }),
  });
}

export function useFeeds() {
  return useQuery({ queryKey: keys.feeds, queryFn: api.feeds, staleTime: 30_000 });
}

export function useFeed(id: string | undefined) {
  const feeds = useFeeds();
  return useQuery({
    queryKey: keys.feed(id ?? ""),
    queryFn: () => api.feed(id!),
    enabled: !!id,
    // Seed from the sidebar list so the header renders instantly.
    initialData: () => feeds.data?.find((f) => f.id === id),
    initialDataUpdatedAt: () => feeds.dataUpdatedAt,
  });
}

export function pinnedFeed(feeds: Feed[] | undefined): Feed | undefined {
  if (!feeds || feeds.length === 0) return undefined;
  return feeds.find((f) => f.pinned) ?? feeds.find((f) => f.id !== EVERYTHING_ID) ?? feeds[0];
}

export function useFeedVideos(id: string, view: "unseen" | "all" | undefined) {
  return useInfiniteQuery({
    queryKey: keys.feedVideos(id, view),
    queryFn: ({ pageParam }) => api.feedVideos(id, view, pageParam),
    ...cursorParams<VideoSummary>(),
  });
}

// Up next is paged so a long playlist scrolls instead of stopping at a fixed
// number of items.
export function useUpNext(id: string, ctx: Record<string, string | undefined>) {
  return useInfiniteQuery({
    queryKey: keys.upNext(id, ctx),
    queryFn: ({ pageParam }) => api.upNext(id, ctx, pageParam),
    staleTime: 60_000,
    ...pageParams<VideoSummary>(),
  });
}

/**
 * A video's archived comments, paged.
 *
 * `enabled` is the section's open state: the first page is fetched when
 * someone actually opens the comments, so a video watched and closed costs
 * nothing. They never change — the archive is a snapshot — so once fetched
 * they are never refetched.
 */
export function useComments(id: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: keys.comments(id),
    queryFn: ({ pageParam }) => api.comments(id, pageParam),
    enabled,
    staleTime: Infinity,
    ...pageParams<Comment>(),
  });
}

export function useSaveFeed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, input }: { id?: string; input: FeedInput }) =>
      id ? api.updateFeed(id, input) : api.createFeed(input),
    onSuccess: () => invalidateFeedish(qc),
  });
}

export function useDeleteFeed() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.deleteFeed(id),
    onSuccess: () => invalidateFeedish(qc),
  });
}

export function invalidateFeedish(qc: QueryClient) {
  void qc.invalidateQueries({ queryKey: ["feeds"] });
  void qc.invalidateQueries({ queryKey: ["channels"] });
}

// After any watch-state change (progress, mark seen, start over) everything
// that shows progress or unseen counts is stale.
export function invalidateWatchState(qc: QueryClient, videoId?: string) {
  void qc.invalidateQueries({ queryKey: ["feeds"] });
  void qc.invalidateQueries({ queryKey: ["channels"] });
  void qc.invalidateQueries({ queryKey: ["playlists"] });
  void qc.invalidateQueries({ queryKey: ["history"] });
  if (videoId) void qc.invalidateQueries({ queryKey: keys.video(videoId) });
}

export function useSetWatched() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, watched }: { id: string; watched: boolean }) => api.setWatched(id, watched),
    onSuccess: (_d, v) => invalidateWatchState(qc, v.id),
  });
}

// After a dismiss/undo: unseen counts change (a dismissed video drops out of
// them), and any cached list that still shows this video — channel,
// playlist, search, history — needs its `dismissed` flag to catch up.
export function invalidateDismissState(qc: QueryClient, videoId?: string) {
  void qc.invalidateQueries({ queryKey: ["feeds"] });
  void qc.invalidateQueries({ queryKey: ["channels"] });
  void qc.invalidateQueries({ queryKey: ["playlists"] });
  void qc.invalidateQueries({ queryKey: ["history"] });
  void qc.invalidateQueries({ queryKey: ["search"] });
  if (videoId) void qc.invalidateQueries({ queryKey: keys.video(videoId) });
}

export function useDismissVideo() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.dismissVideo(id),
    onSuccess: (_d, id) => invalidateDismissState(qc, id),
  });
}

export function useUndismissVideo() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.undismissVideo(id),
    onSuccess: (_d, id) => invalidateDismissState(qc, id),
  });
}

export function useChannels(q: string, sort: "name" | "videos" | "unseen" | "last_upload", unfeeded: boolean) {
  return useInfiniteQuery({
    queryKey: keys.channels(q, sort, unfeeded),
    queryFn: ({ pageParam }) => api.channels({ q, sort, unfeeded, page: pageParam }),
    ...pageParams(),
  });
}

// Whole directory for pickers (feed editor); pages through to the end.
export function useAllChannels() {
  return useQuery({
    queryKey: ["channels", "all"],
    queryFn: async () => {
      const out = [];
      let page = 0;
      for (;;) {
        const p = await api.channels({ sort: "name", page, page_size: 100 });
        out.push(...p.items);
        const more = p.has_more ?? (p.page + 1) * p.page_size < p.total;
        if (!more || p.items.length === 0) break;
        page = p.page + 1;
      }
      return out;
    },
    staleTime: 60_000,
  });
}

export function useChannel(id: string) {
  return useQuery({ queryKey: keys.channel(id), queryFn: () => api.channel(id) });
}

export function useChannelVideos(id: string, view: "all" | "unseen") {
  return useInfiniteQuery({
    queryKey: keys.channelVideos(id, view),
    queryFn: ({ pageParam }) => api.channelVideos(id, view, pageParam),
    ...cursorParams<VideoSummary>(),
  });
}

export function useChannelPlaylists(id: string, enabled = true) {
  return useQuery({ queryKey: keys.channelPlaylists(id), queryFn: () => api.channelPlaylists(id), enabled });
}

export function useVideo(id: string) {
  return useQuery({ queryKey: keys.video(id), queryFn: () => api.video(id) });
}

// Chapters never change for a downloaded file, so cache them forever once
// fetched. A missing/failed response must degrade silently to "no chapter
// UI" — never retried into an error state, never a blocking spinner — so
// callers should read `.data?.chapters ?? []` and ignore isError/isLoading.
export function useChapters(id: string) {
  return useQuery({
    queryKey: keys.chapters(id),
    queryFn: () => api.chapters(id),
    staleTime: Infinity,
    retry: false,
  });
}

export function usePlaylists(kind: "custom" | "channel" | undefined) {
  return useInfiniteQuery({
    queryKey: keys.playlists(kind),
    queryFn: ({ pageParam }) => api.playlists(kind, pageParam),
    ...pageParams(),
  });
}

export function usePlaylist(id: string) {
  return useQuery({ queryKey: keys.playlist(id), queryFn: () => api.playlist(id) });
}

export function usePinnedPlaylists() {
  return useQuery({ queryKey: keys.pinnedPlaylists, queryFn: api.pinnedPlaylists, staleTime: 30_000 });
}

export function useSetPlaylistPinned() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, pinned }: { id: string; pinned: boolean }) => api.setPlaylistPinned(id, pinned),
    onSuccess: (_d, v) => {
      void qc.invalidateQueries({ queryKey: keys.pinnedPlaylists });
      void qc.invalidateQueries({ queryKey: ["playlists"] });
      void qc.invalidateQueries({ queryKey: keys.playlist(v.id) });
    },
  });
}

export function useSetPlaylistMusic() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, music }: { id: string; music: boolean }) => api.setPlaylistMusic(id, music),
    onSuccess: (_d, v) => {
      void qc.invalidateQueries({ queryKey: keys.pinnedPlaylists });
      void qc.invalidateQueries({ queryKey: ["playlists"] });
      void qc.invalidateQueries({ queryKey: keys.playlist(v.id) });
    },
  });
}

// The sidebar's "Continue watching" list. Only the first page is fetched — the
// History page is where the full list lives.
export function useInProgress() {
  return useQuery({
    queryKey: keys.inProgress,
    queryFn: () => api.history("in_progress", "", 0),
    staleTime: 30_000,
  });
}

export function useRemoveHistoryEntry() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (entryId: string) => api.deleteHistory(entryId),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["history"] });
    },
  });
}

export function useHistory(filter: "all" | "in_progress" | "seen", q: string) {
  return useInfiniteQuery({
    queryKey: keys.history(filter, q),
    queryFn: ({ pageParam }) => api.history(filter, q, pageParam),
    ...pageParams(),
  });
}

export function useSearch(q: string, scope: "all" | "titles" | "subtitles" | "channels" | "playlists", unseen: boolean, feed: string | undefined) {
  return useQuery({
    queryKey: keys.search(q, scope, unseen, feed),
    queryFn: () => api.search(q, { scope, unseen, feed }),
    enabled: q.trim().length > 0,
    staleTime: 30_000,
  });
}
