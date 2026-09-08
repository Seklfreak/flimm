import { useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, type PlaylistSummary } from "@/lib/api";
import { invalidateFeedish, invalidateWatchState, keys, useChannel, useChannelPlaylists, useChannelVideos, useMe, useSetChannelPinned } from "@/lib/queries";
import { plural, relativeDay } from "@/lib/format";
import { Avatar, EmptyState, ErrorState, InfiniteSentinel, PinIcon, Segmented, Spinner } from "@/components/ui";
import { InFeedsControl } from "@/components/InFeedsControl";
import { VideoCard, VideoGrid } from "@/components/VideoCard";
import { PlaylistCard } from "@/pages/PlaylistsPage";

type Tab = "all" | "unseen" | "playlists";

export default function ChannelPage() {
  const { id = "" } = useParams();
  const channel = useChannel(id);
  // The tab lives in the URL (?tab=unseen|playlists) so the browser's back
  // button steps back through tabs and a tab can be linked to; the default
  // "all" stays out of the address bar.
  const [params, setParams] = useSearchParams();
  const rawTab = params.get("tab");
  const tab: Tab = rawTab === "unseen" || rawTab === "playlists" ? rawTab : "all";
  const setTab = (next: Tab) =>
    setParams(next === "all" ? {} : { tab: next });
  const videos = useChannelVideos(id, tab === "unseen" ? "unseen" : "all");
  const playlists = useChannelPlaylists(id, tab === "playlists");
  const qc = useQueryClient();
  const [marking, setMarking] = useState(false);
  const me = useMe();
  const setPinned = useSetChannelPinned();
  const [subscribing, setSubscribing] = useState(false);
  // The admin's "Find series": the request holds while TubeArchivist runs
  // its discovery; "still" is the slow case (a big channel), followed on the
  // status endpoint until the task is gone; "none" is a discovery that ran
  // and found nothing, which is not the same as one that never ran.
  const [indexing, setIndexing] = useState<"idle" | "waiting" | "still" | "none" | { error: string }>("idle");
  const settleIndexing = async () => {
    const found = await qc.fetchQuery({ queryKey: keys.channelPlaylists(id), queryFn: () => api.channelPlaylists(id) });
    setIndexing(found.length === 0 ? "none" : "idle");
  };
  const findSeries = async () => {
    setIndexing("waiting");
    try {
      const result = await api.indexChannelPlaylists(id);
      if (result.status === "pending") {
        setIndexing("still");
        return;
      }
      qc.setQueryData(keys.channelPlaylists(id), result.playlists);
      setIndexing(result.playlists.length === 0 ? "none" : "idle");
    } catch (e) {
      setIndexing({ error: e instanceof ApiError ? e.message : "Could not reach the server." });
    }
  };
  useEffect(() => {
    if (indexing !== "still") return;
    let cancelled = false;
    const timer = setInterval(() => {
      void api.playlistIndexingStatus(id).then(({ status }) => {
        if (cancelled || status === "running") return;
        clearInterval(timer);
        void settleIndexing();
      }).catch(() => {});
    }, 5000);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [indexing, id]);

  if (channel.isError) return <ErrorState message={channel.error.message} retry={() => channel.refetch()} />;
  const c = channel.data;
  const items = videos.data?.pages.flatMap((p) => p.items) ?? [];

  const markAll = async () => {
    setMarking(true);
    try {
      await api.markChannelSeen(id);
      invalidateWatchState(qc);
      void qc.invalidateQueries({ queryKey: ["channels", id] });
    } finally {
      setMarking(false);
    }
  };

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-6">
      <div className="flex flex-col gap-4 px-5 pt-[max(20px,env(safe-area-inset-top))] md:gap-6 md:px-10 md:pt-8">
        <div className="flex items-center gap-2 text-[13px] font-semibold text-muted-2">
          <Link to="/channels" className="text-muted-2 no-underline hover:text-ink">Channels</Link>
          <span>/</span>
          <span className="truncate text-ink">{c?.name ?? "…"}</span>
        </div>
        <div className="flex flex-wrap items-center gap-4 md:gap-5">
          <Avatar src={c?.thumb_url} name={c?.name ?? "?"} size={72} />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="h1 truncate">{c?.name ?? "…"}</span>
            {c && (
              <span className="meta">
                {plural(c.video_count, "video")} · {c.unseen_count} unseen
                {c.last_upload && ` · last upload ${relativeDay(c.last_upload)}`}
              </span>
            )}
          </div>
          {c && (
            <div className="flex items-center gap-2">
              {me.data?.is_admin && (
                <button
                  className={`seg ${c.subscribed ? "on" : ""}`}
                  aria-pressed={c.subscribed}
                  title={
                    c.subscribed
                      ? "The archive downloads this channel's new videos — click to unsubscribe"
                      : "Subscribe: the archive will download this channel's new videos"
                  }
                  disabled={subscribing}
                  onClick={() => {
                    setSubscribing(true);
                    api
                      .setChannelSubscribed(c.id, !c.subscribed)
                      .then(() => qc.invalidateQueries({ queryKey: ["channels"] }))
                      .finally(() => setSubscribing(false));
                  }}
                >
                  {c.subscribed ? "Subscribed ✓" : "Subscribe"}
                </button>
              )}
              <button
                className={`seg ${c.pinned ? "on" : ""}`}
                aria-pressed={c.pinned}
                aria-label={c.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
                title={c.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
                onClick={() => setPinned.mutate({ id: c.id, pinned: !c.pinned })}
                disabled={setPinned.isPending}
              >
                <PinIcon />
              </button>
              <InFeedsControl
                feedIds={c.feeds.map((f) => f.id)}
                onSave={async (ids) => {
                  await api.setChannelFeeds(c.id, ids);
                  await qc.invalidateQueries({ queryKey: ["channels", c.id] });
                  invalidateFeedish(qc);
                }}
              />
              <button className="seg" onClick={() => void markAll()} disabled={marking || c.unseen_count === 0}>
                {marking ? "Marking…" : "Mark all seen"}
              </button>
            </div>
          )}
        </div>
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: "all", label: "All videos" },
            { value: "unseen", label: "Unseen" },
            { value: "playlists", label: "Playlists" },
          ]}
        />
      </div>
      <div className="px-5 md:px-10">
        {tab === "playlists" ? (
          playlists.isLoading ? (
            <Spinner label="Loading playlists…" />
          ) : (playlists.data ?? []).length === 0 ? (
            <div className="flex flex-col items-center gap-3">
              <EmptyState
                title={indexing === "none" ? "TubeArchivist found no playlists on this channel" : "No playlists archived from this channel"}
                hint={me.data?.is_admin && indexing !== "none" ? "TubeArchivist has not indexed this channel's playlists (series)." : undefined}
              />
              {me.data?.is_admin &&
                (indexing === "waiting" ? (
                  <Spinner label="TubeArchivist is indexing this channel's playlists…" />
                ) : indexing === "still" ? (
                  <Spinner label="Still indexing — a big channel takes a few minutes. This updates when it lands." />
                ) : indexing === "none" ? null : (
                  <>
                    <button className="btn" onClick={() => void findSeries()}>
                      Find series (index playlists)
                    </button>
                    {typeof indexing === "object" && <p className="meta text-danger">{indexing.error}</p>}
                  </>
                ))}
            </div>
          ) : (
            <VideoGrid>
              {(playlists.data ?? []).map((p: PlaylistSummary) => (
                <PlaylistCard key={p.id} playlist={p} />
              ))}
            </VideoGrid>
          )
        ) : videos.isLoading ? (
          <Spinner label="Loading videos…" />
        ) : videos.isError ? (
          <ErrorState message={videos.error.message} retry={() => videos.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState title={tab === "unseen" ? "All caught up" : "No videos archived yet"} />
        ) : (
          <>
            <VideoGrid>
              {items.map((v) => (
                <VideoCard key={v.id} video={v} ctx={{ channel: id }} showChannel={false} />
              ))}
            </VideoGrid>
            <InfiniteSentinel enabled={!!videos.hasNextPage && !videos.isFetchingNextPage} onVisible={() => void videos.fetchNextPage()} />
            {videos.isFetchingNextPage && <div className="py-6"><Spinner /></div>}
          </>
        )}
      </div>
    </div>
  );
}
