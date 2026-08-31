import { useCallback, useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { EVERYTHING_ID, type FeedView, type VideoSummary } from "@/lib/api";
import { useDismissVideo, useFeed, useFeedVideos, useUndismissVideo } from "@/lib/queries";
import { plural } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { EmptyState, ErrorState, InfiniteSentinel, Segmented, Spinner } from "@/components/ui";
import { DismissedCard, VideoCard, VideoGrid } from "@/components/VideoCard";
import { FeedEditor } from "@/components/FeedEditor";

// No "In progress" filter: the unseen view opens with what is half-watched,
// so there is nothing left for it to find (docs/api.md, `view=`).
const VIEWS: { value: FeedView; label: string }[] = [
  { value: "unseen", label: "Unseen" },
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

  // A feed never returns a dismissed video (docs/api.md "dismissed"), so
  // dismissing here has to pull the card out of this list itself rather than
  // toggling a flag in place.
  //
  // The undo slot is held *outside* the fetched list on purpose. Dismissing
  // invalidates the feed query, so the refetch comes back without that video —
  // and an undo rendered from `items` would vanish a moment after it appeared,
  // which is the whole affordance gone. Keeping the video and the index it sat
  // at means the slot survives the refetch and Undo can put the same object
  // straight back.
  // `undo` is a card that has just been dismissed and can be brought back;
  // `restored` is one the viewer brought back, held until the refetch returns
  // it, so Undo puts the card on screen at once rather than after a round trip.
  const [pending, setPending] = useState<{ video: VideoSummary; index: number; mode: "undo" | "restored" }[]>([]);
  const dismiss = useDismissVideo();
  const undismiss = useUndismissVideo();
  const onDismiss = useCallback(
    (v: VideoSummary, index: number) => {
      setPending((list) => [...list, { video: v, index, mode: "undo" as const }]);
      dismiss.mutate(v.id, {
        onError: () => setPending((list) => list.filter((d) => d.video.id !== v.id)),
      });
    },
    [dismiss],
  );
  // A restored slot is a stand-in until the refetch returns the real card.
  // Dropping it then keeps one video from being rendered out of stale state
  // forever; returning the same array when nothing changed is what stops this
  // from looping on every render.
  const itemIds = (videos.data?.pages.flatMap((p) => p.items) ?? []).map((v) => v.id).join(",");
  useEffect(() => {
    const present = new Set(itemIds ? itemIds.split(",") : []);
    setPending((list) => {
      const next = list.filter((p) => !(p.mode === "restored" && present.has(p.video.id)));
      return next.length === list.length ? list : next;
    });
  }, [itemIds]);

  const onUndo = useCallback(
    (v: VideoSummary) => {
      setPending((list) => list.map((d) => (d.video.id === v.id ? { ...d, mode: "restored" as const } : d)));
      undismiss.mutate(v.id, {
        onError: () => setPending((list) => list.map((d) => (d.video.id === v.id ? { ...d, mode: "undo" as const } : d))),
      });
    },
    [undismiss],
  );

  if (feed.isError) return <ErrorState message={feed.error.message} retry={() => feed.refetch()} />;
  const f = feed.data;
  const items = videos.data?.pages.flatMap((p) => p.items) ?? [];
  // What the grid actually renders: the fetched list with the pending slots
  // put back where their cards were. Filtering by id first covers both
  // in-between states — the moment after a dismissal before the refetch drops
  // the video, and the moment after an undo before the refetch returns it.
  const pendingIds = new Set(pending.map((p) => p.video.id));
  const slots: { video: VideoSummary; undo?: true }[] = items
    .filter((v) => !pendingIds.has(v.id))
    .map((v) => ({ video: v }));
  for (const { video, index, mode } of pending) {
    slots.splice(Math.min(index, slots.length), 0, mode === "undo" ? { video, undo: true } : { video });
  }
  const total = videos.data?.pages[0]?.total;

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-[22px]">
      <PageHeader
        title={f?.name ?? "…"}
        feedPicker
        meta={
          f && (
            <>
              {plural(f.unseen_count, "unseen", "unseen")}
              {f.id === EVERYTHING_ID ? <> · all {plural(f.channel_count, "channel")}</> : <>
                {(f.channel_count > 0 || f.playlist_count === 0) && <> · {plural(f.channel_count, "channel")}</>}
                {f.playlist_count > 0 && <> · {plural(f.playlist_count, "series", "series")}</>}
              </>}
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
            title={effectiveView === "unseen" ? "All caught up" : "No videos here yet"}
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
              {slots.map((slot) =>
                slot.undo ? (
                  <DismissedCard key={slot.video.id} video={slot.video} onUndo={() => onUndo(slot.video)} />
                ) : (
                  <VideoCard
                    key={slot.video.id}
                    video={slot.video}
                    ctx={{ feed: id }}
                    onDismiss={(v) => onDismiss(v, slots.indexOf(slot))}
                  />
                ),
              )}
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
