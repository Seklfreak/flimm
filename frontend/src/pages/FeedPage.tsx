import { useCallback, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { EVERYTHING_ID, type FeedView } from "@/lib/api";
import { useFeed, useFeedVideos } from "@/lib/queries";
import { plural } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { EmptyState, ErrorState, InfiniteSentinel, Segmented, Spinner } from "@/components/ui";
import { VideoCard, VideoGrid } from "@/components/VideoCard";
import { FeedEditor } from "@/components/FeedEditor";

const VIEWS: { value: FeedView; label: string }[] = [
  { value: "unseen", label: "Unseen" },
  { value: "continue", label: "In progress" },
  { value: "all", label: "Everything" },
];

export default function FeedPage({ editing = false }: { editing?: boolean }) {
  const { id = "" } = useParams();
  const navigate = useNavigate();
  const feed = useFeed(id);
  // Default view follows the feed's hide_seen option until the user picks one.
  const [view, setView] = useState<FeedView | undefined>(undefined);
  const effectiveView: FeedView = view ?? (feed.data ? (feed.data.hide_seen ? "unseen" : "all") : "unseen");
  const videos = useFeedVideos(id, view);
  const closeEditor = useCallback(() => navigate(`/feeds/${id}`, { replace: true }), [navigate, id]);

  if (feed.isError) return <ErrorState message={feed.error.message} retry={() => feed.refetch()} />;
  const f = feed.data;
  const items = videos.data?.pages.flatMap((p) => p.items) ?? [];
  const total = videos.data?.pages[0]?.total;

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-[22px]">
      <PageHeader
        title={f?.name ?? "…"}
        feedPicker
        meta={
          f && (
            <>
              {plural(f.unseen_count, "unseen", "unseen")} · {f.id === EVERYTHING_ID ? `all ${plural(f.channel_count, "channel")}` : plural(f.channel_count, "channel")}
              {" · "}
              <Link to={`/feeds/${f.id}/edit`} className="!font-medium !text-accent">
                {f.id === EVERYTHING_ID ? "options" : "edit feed"}
              </Link>
            </>
          )
        }
        actions={<Segmented value={effectiveView} onChange={setView} options={VIEWS} />}
      />
      <div className="px-5 md:px-10">
        {videos.isLoading ? (
          <Spinner label="Loading videos…" />
        ) : videos.isError ? (
          <ErrorState message={videos.error.message} retry={() => videos.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState
            title={effectiveView === "unseen" ? "All caught up" : effectiveView === "continue" ? "Nothing in progress" : "No videos here yet"}
            hint={effectiveView === "unseen" && total === 0 ? "Everything in this feed has been seen." : undefined}
            action={
              effectiveView !== "all" ? (
                <button className="btn mt-2" onClick={() => setView("all")}>
                  Show everything
                </button>
              ) : undefined
            }
          />
        ) : (
          <>
            <VideoGrid>
              {items.map((v) => (
                <VideoCard key={v.id} video={v} ctx={{ feed: id }} />
              ))}
            </VideoGrid>
            <InfiniteSentinel enabled={!!videos.hasNextPage && !videos.isFetchingNextPage} onVisible={() => void videos.fetchNextPage()} />
            {videos.isFetchingNextPage && <div className="py-6"><Spinner /></div>}
          </>
        )}
      </div>
      {editing && f && <FeedEditor feed={f} onClose={closeEditor} />}
    </div>
  );
}
