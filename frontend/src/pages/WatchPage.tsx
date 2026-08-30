import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, EVERYTHING_ID } from "@/lib/api";
import { invalidateWatchState, keys, useChapters, useComments, useFeeds, usePrefs, useSetWatched, useUpdatePrefs, useUpNext, useVideo } from "@/lib/queries";
import { compactCount, fmtDuration, plural, relativeDay } from "@/lib/format";
import { Avatar, CheckIcon, ErrorState, Spinner, ThumbIcon } from "@/components/ui";
import { watchHref } from "@/components/VideoCard";
import { Player, SUBTITLE_OFF, langName, pickTrack, type PlayerHandle } from "@/player/Player";
import { Chapters } from "@/player/Chapters";
import { Comments } from "@/player/Comments";
import { AddToPlaylist } from "@/player/AddToPlaylist";
import { UpNextPanel } from "@/player/UpNextPanel";

export default function WatchPage() {
  const { id = "" } = useParams();
  const [params, setParams] = useSearchParams();
  // `audio=1` is the live audio/video mode. It seeds from the playlist's
  // `music` flag via the ctx every link already carries (see PlaylistPage) —
  // this page only ever reads the param, it never fetches the playlist to
  // re-derive it.
  const audioOnly = params.get("audio") === "1";
  const ctx = useMemo(
    () => ({
      feed: params.get("feed") ?? undefined,
      playlist: params.get("playlist") ?? undefined,
      channel: params.get("channel") ?? undefined,
      // Carried through every next/previous link so a shuffled run keeps its
      // order across navigations and survives a reload.
      shuffle: params.get("shuffle") ?? undefined,
      audio: params.get("audio") ?? undefined,
    }),
    [params],
  );
  const t = params.get("t");
  const startAt = t !== null && !Number.isNaN(Number(t)) ? Number(t) : undefined;
  const onToggleAudioOnly = useCallback(() => {
    const next = new URLSearchParams(params);
    if (audioOnly) next.delete("audio");
    else next.set("audio", "1");
    setParams(next, { replace: true });
  }, [params, audioOnly, setParams]);

  const video = useVideo(id);
  const prefs = usePrefs();
  const updatePrefs = useUpdatePrefs();
  const setWatched = useSetWatched();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const feeds = useFeeds();
  const upNext = useUpNext(id, ctx);
  const upNextItems = useMemo(() => (upNext.data?.pages ?? []).flatMap((p) => p.items), [upNext.data]);
  // Neighbours in the playlist/feed/channel the video was opened from. Only
  // requested when there is such a context; without one there is nothing to
  // step through and the player hides the buttons.
  const hasContext = Boolean(ctx.feed || ctx.playlist || ctx.channel);
  const nav = useQuery({
    queryKey: keys.nav(id, ctx),
    queryFn: () => api.nav(id, ctx),
    enabled: hasContext,
    staleTime: 60_000,
  });
  const chapters = useChapters(id).data?.chapters ?? [];
  // The comments load when the section is opened and not before: they are the
  // longest thing on the page and the least often wanted.
  const [commentsOpen, setCommentsOpen] = useState(false);
  const comments = useComments(id, commentsOpen);
  const commentPages = comments.data?.pages.flatMap((p) => p.items) ?? [];
  const [activeChapter, setActiveChapter] = useState(-1);
  const playerRef = useRef<PlayerHandle>(null);
  useEffect(() => {
    setActiveChapter(-1);
    window.scrollTo({ top: 0 });
  }, [id]);

  const v = video.data;
  const onPrefs = useCallback((patch: Parameters<typeof updatePrefs.mutate>[0]) => updatePrefs.mutate(patch), [updatePrefs]);
  const onWatched = useCallback(() => invalidateWatchState(qc, id), [qc, id]);
  const onStartOver = useCallback(async () => {
    await api.startOver(id);
    invalidateWatchState(qc, id);
  }, [qc, id]);
  const onSeekChapter = useCallback((t: number) => playerRef.current?.seek(t), []);
  const next = upNextItems[0];
  const onEnded = useCallback(() => {
    if (prefs?.autoplay && next) navigate(watchHref(next, ctx));
  }, [prefs?.autoplay, next, navigate, ctx]);

  if (video.isError) return <ErrorState message={video.error.message} retry={() => video.refetch()} />;
  if (!v || !prefs) return <div className="p-10"><Spinner label="Loading video…" /></div>;

  const contextName = ctx.feed
    ? feeds.data?.find((f) => f.id === ctx.feed)?.name
    : ctx.playlist
      ? (v.playlists.find((p) => p.id === ctx.playlist)?.name ?? "playlist")
      : ctx.channel
        ? v.channel.name
        : undefined;
  const track = pickTrack(v.subtitles, prefs.subtitle_lang);
  const channelFeeds = v.channel.feeds.filter((f) => f.id !== EVERYTHING_ID).map((f) => f.name);
  const desc = v.description ?? "";

  return (
    <div className="flex flex-col gap-6 px-0 pb-10 pt-0 md:flex-row md:gap-8 md:px-10 md:pt-8">
      <div className="flex min-w-0 flex-1 flex-col gap-4 md:gap-[18px]">
        <div className="md:rounded-2xl">
          <Player
            key={v.id}
            ref={playerRef}
            video={v}
            prefs={prefs}
            startAt={startAt}
            onPrefs={onPrefs}
            onWatched={onWatched}
            onStartOver={onStartOver}
            onEnded={onEnded}
            onChapterChange={setActiveChapter}
            audioOnly={audioOnly}
            onToggleAudioOnly={onToggleAudioOnly}
            playlistId={ctx.playlist}
            nav={
              hasContext
                ? {
                    onPrev: nav.data?.previous ? () => navigate(watchHref(nav.data!.previous!, ctx)) : undefined,
                    onNext: nav.data?.next ? () => navigate(watchHref(nav.data!.next!, ctx)) : undefined,
                  }
                : undefined
            }
          />
        </div>
        <div className="flex flex-col gap-4 px-5 md:gap-[18px] md:px-0">
          <div className="flex flex-col gap-1.5">
            <h1 className="text-[20px] font-extrabold leading-[1.2] tracking-[-0.02em] md:text-[24px]">{v.title}</h1>
            <span className="meta">
              {fmtDuration(v.duration)}
              {v.height ? ` · ${v.height}p` : ""} · added {relativeDay(v.downloaded)} ·{" "}
              <a href={v.youtube_url} target="_blank" rel="noreferrer" className="!font-medium !text-accent">
                watch on YouTube
              </a>
            </span>
            {/* Views and votes. Counts, not controls: nothing here can vote on
                YouTube's behalf, and the dislike half only exists when the
                deployment turned on Return YouTube Dislike (docs/api.md
                "Views and votes"). */}
            {(v.stats.views > 0 || v.stats.likes > 0 || v.stats.dislikes !== undefined) && (
              <span className="meta flex items-center gap-3" aria-label="view and vote counts">
                {v.stats.views > 0 && <span>{compactCount(v.stats.views)} views</span>}
                {/* An archive that recorded no likes shows none, rather than a
                    thumb reading zero. A real zero — the service knows the
                    video and it has none — comes with a dislike count. */}
                {(v.stats.likes > 0 || v.stats.dislikes !== undefined) && (
                  <span className="flex items-center gap-1.5">
                    <ThumbIcon />
                    {compactCount(v.stats.likes)}
                  </span>
                )}
                {v.stats.dislikes !== undefined && (
                  <span className="flex items-center gap-1.5">
                    <ThumbIcon down />
                    {compactCount(v.stats.dislikes)}
                  </span>
                )}
              </span>
            )}
          </div>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex items-center gap-3">
              <Avatar src={v.channel.thumb_url} name={v.channel.name} />
              <div className="flex flex-col">
                <Link to={`/channels/${v.channel.id}`} className="text-[15px] font-extrabold text-ink no-underline hover:text-ink">
                  {v.channel.name}
                </Link>
                <span className="meta text-[12px]">
                  {plural(v.channel.video_count, "video")}
                  {channelFeeds.length > 0 ? ` · in ${channelFeeds.join(", ")}` : " · not in a feed"}
                </span>
              </div>
            </div>
            <div className="flex flex-wrap gap-2">
              {/* Marking a song seen is meaningless — a music playlist's playback
                  deliberately records no watch state (docs/api.md "Music playlists"). */}
              {!audioOnly && (
                <button
                  className={`btn ${v.watched ? "" : "pri"}`}
                  onClick={() => setWatched.mutate({ id: v.id, watched: !v.watched })}
                  disabled={setWatched.isPending}
                >
                  <CheckIcon size={14} />
                  {v.watched ? "Seen" : "Mark seen"}
                </button>
              )}
              <AddToPlaylist videoId={v.id} memberOf={v.playlists} />
              <button className="btn" onClick={() => onPrefs({ subtitle_lang: track ? SUBTITLE_OFF : (v.subtitles[0]?.lang ?? SUBTITLE_OFF) })} disabled={v.subtitles.length === 0}>
                CC · {track ? langName(track.lang) : v.subtitles.length === 0 ? "none" : "Off"}
              </button>
            </div>
          </div>
          {desc && (
            <div className="rounded-[14px] bg-raised-2 p-4 text-[14px] font-medium leading-[1.5] text-ink-2">
              <span className="whitespace-pre-wrap">{desc}</span>
            </div>
          )}
          <Chapters chapters={chapters} activeIndex={activeChapter} onSeek={onSeekChapter} />
          <Comments
            comments={commentPages}
            total={comments.data?.pages[0]?.total ?? 0}
            isLoading={comments.isLoading}
            hasMore={!!comments.hasNextPage}
            isFetchingMore={comments.isFetchingNextPage}
            fetchMore={() => void comments.fetchNextPage()}
            open={commentsOpen}
            onToggle={setCommentsOpen}
          />
        </div>
      </div>
      <UpNextPanel
        items={upNextItems}
        title={contextName ? `Up next in ${contextName}` : "Up next"}
        isLoading={upNext.isLoading}
        hasNextPage={!!upNext.hasNextPage}
        isFetchingNextPage={upNext.isFetchingNextPage}
        fetchNextPage={() => void upNext.fetchNextPage()}
        ctx={ctx}
        autoplay={!!prefs?.autoplay}
        onAutoplay={onPrefs}
      />
    </div>
  );
}
