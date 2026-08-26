import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams, useSearchParams } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, EVERYTHING_ID, type VideoSummary } from "@/lib/api";
import { invalidateWatchState, keys, useFeeds, usePrefs, useSetWatched, useUpdatePrefs, useVideo } from "@/lib/queries";
import { fmtDuration, plural, relativeDay } from "@/lib/format";
import { Avatar, CheckIcon, ErrorState, Spinner, Toggle } from "@/components/ui";
import { Thumb, watchHref } from "@/components/VideoCard";
import { Player, langName, pickTrack } from "@/player/Player";
import { AddToPlaylist } from "@/player/AddToPlaylist";

export default function WatchPage() {
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const ctx = useMemo(
    () => ({ feed: params.get("feed") ?? undefined, playlist: params.get("playlist") ?? undefined, channel: params.get("channel") ?? undefined }),
    [params],
  );
  const t = params.get("t");
  const startAt = t !== null && !Number.isNaN(Number(t)) ? Number(t) : undefined;

  const video = useVideo(id);
  const prefs = usePrefs();
  const updatePrefs = useUpdatePrefs();
  const setWatched = useSetWatched();
  const qc = useQueryClient();
  const navigate = useNavigate();
  const feeds = useFeeds();
  const upNext = useQuery({ queryKey: keys.upNext(id, ctx), queryFn: () => api.upNext(id, ctx), staleTime: 60_000 });
  const [expanded, setExpanded] = useState(false);
  useEffect(() => {
    setExpanded(false);
    window.scrollTo({ top: 0 });
  }, [id]);

  const v = video.data;
  const onPrefs = useCallback((patch: Parameters<typeof updatePrefs.mutate>[0]) => updatePrefs.mutate(patch), [updatePrefs]);
  const onWatched = useCallback(() => invalidateWatchState(qc, id), [qc, id]);
  const onStartOver = useCallback(async () => {
    await api.startOver(id);
    invalidateWatchState(qc, id);
  }, [qc, id]);
  const next = upNext.data?.[0];
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
  const long = desc.length > 320;

  return (
    <div className="flex flex-col gap-6 px-0 pb-10 pt-0 md:flex-row md:gap-8 md:px-10 md:pt-8">
      <div className="flex min-w-0 flex-1 flex-col gap-4 md:gap-[18px]">
        <div className="md:rounded-2xl">
          <Player key={v.id} video={v} prefs={prefs} startAt={startAt} onPrefs={onPrefs} onWatched={onWatched} onStartOver={onStartOver} onEnded={onEnded} />
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
              <button
                className={`btn ${v.watched ? "" : "pri"}`}
                onClick={() => setWatched.mutate({ id: v.id, watched: !v.watched })}
                disabled={setWatched.isPending}
              >
                <CheckIcon size={14} />
                {v.watched ? "Seen" : "Mark seen"}
              </button>
              <AddToPlaylist videoId={v.id} memberOf={v.playlists} />
              <button className="btn" onClick={() => onPrefs({ subtitle_lang: track ? null : (v.subtitles[0]?.lang ?? null) })} disabled={v.subtitles.length === 0}>
                CC · {track ? langName(track.lang) : v.subtitles.length === 0 ? "none" : "Off"}
              </button>
            </div>
          </div>
          {desc && (
            <div className="rounded-[14px] bg-raised-2 p-4 text-[14px] font-medium leading-[1.5] text-ink-2">
              <span className={expanded ? "whitespace-pre-wrap" : "line-clamp-4 whitespace-pre-wrap"}>{desc}</span>{" "}
              {long && (
                <button className="font-bold text-accent" onClick={() => setExpanded((e) => !e)}>
                  {expanded ? "Show less" : "Show more"}
                </button>
              )}
            </div>
          )}
        </div>
      </div>
      <aside className="flex w-full flex-none flex-col gap-3.5 px-5 md:w-[360px] md:px-0">
        <div className="flex items-baseline justify-between">
          <span className="text-[16px] font-extrabold">{contextName ? `Up next in ${contextName}` : "Up next"}</span>
          <label className="flex items-center gap-2 meta text-[12px]">
            Autoplay {prefs.autoplay ? "on" : "off"}
            <Toggle on={prefs.autoplay} onChange={(on) => onPrefs({ autoplay: on })} label="Autoplay" />
          </label>
        </div>
        {upNext.isLoading ? (
          <Spinner />
        ) : (upNext.data ?? []).length === 0 ? (
          <p className="meta">Nothing more in this context.</p>
        ) : (
          (upNext.data ?? []).map((n: VideoSummary) => (
            <Link key={n.id} to={watchHref(n, ctx)} className="flex items-center gap-3 text-ink no-underline hover:text-ink">
              <div className="w-32 flex-none"><Thumb video={n} compact className="!rounded-[10px]" /></div>
              <span className="flex min-w-0 flex-col gap-[3px]">
                <span className="text-[14px] font-extrabold leading-[1.25] line-clamp-2">{n.title}</span>
                <span className="meta text-[12px]">
                  {n.channel.name} · {fmtDuration(n.duration)}
                </span>
              </span>
            </Link>
          ))
        )}
      </aside>
    </div>
  );
}
