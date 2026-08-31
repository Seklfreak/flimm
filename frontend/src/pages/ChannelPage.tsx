import { useState } from "react";
import { Link, useParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, type PlaylistSummary } from "@/lib/api";
import { invalidateFeedish, invalidateWatchState, useChannel, useChannelPlaylists, useChannelVideos, useMe } from "@/lib/queries";
import { plural, relativeDay } from "@/lib/format";
import { Avatar, EmptyState, ErrorState, InfiniteSentinel, Segmented, Spinner } from "@/components/ui";
import { InFeedsControl } from "@/components/InFeedsControl";
import { VideoCard, VideoGrid } from "@/components/VideoCard";
import { PlaylistCard } from "@/pages/PlaylistsPage";

type Tab = "all" | "unseen" | "playlists";

export default function ChannelPage() {
  const { id = "" } = useParams();
  const channel = useChannel(id);
  const [tab, setTab] = useState<Tab>("all");
  const videos = useChannelVideos(id, tab === "unseen" ? "unseen" : "all");
  const playlists = useChannelPlaylists(id, tab === "playlists");
  const qc = useQueryClient();
  const [marking, setMarking] = useState(false);
  const me = useMe();
  // "requested" survives until the page is left: TubeArchivist discovers the
  // playlists in a background task, so there is nothing to await here.
  const [indexRequested, setIndexRequested] = useState(false);

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
                title="No playlists archived from this channel"
                hint={me.data?.is_admin ? "TubeArchivist has not indexed this channel's playlists (series)." : undefined}
              />
              {me.data?.is_admin &&
                (indexRequested ? (
                  <p className="meta">Asked TubeArchivist to index them — the discovery runs there and can take a few minutes. Check back later.</p>
                ) : (
                  <button
                    className="btn"
                    onClick={() => {
                      setIndexRequested(true);
                      api.indexChannelPlaylists(id).catch(() => setIndexRequested(false));
                    }}
                  >
                    Find series (index playlists)
                  </button>
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
