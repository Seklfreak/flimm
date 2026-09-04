import { useCallback, useEffect, useState } from "react";
import { Link } from "react-router";
import type { Prefs, VideoSummary } from "@/lib/api";
import { useDismissVideo, useUndismissVideo } from "@/lib/queries";
import { fmtDuration } from "@/lib/format";
import { Thumb, watchHref } from "@/components/VideoCard";
import { ChevronIcon, CloseIcon, InfiniteSentinel, Spinner, Toggle } from "@/components/ui";

/**
 * Whether the sidebar is collapsed. A layout preference for this browser
 * rather than an account one — the phone and TV have no sidebar to collapse,
 * so it would mean nothing to them — which is why it lives in `localStorage`
 * beside the quality choice instead of in `PATCH /me/prefs`.
 */
export const UP_NEXT_STORAGE_KEY = "upNextCollapsed";

export function loadCollapsed(): boolean {
  try {
    return window.localStorage.getItem(UP_NEXT_STORAGE_KEY) === "1";
  } catch {
    return false; // private mode, or storage the browser refuses
  }
}

function saveCollapsed(collapsed: boolean): void {
  try {
    window.localStorage.setItem(UP_NEXT_STORAGE_KEY, collapsed ? "1" : "0");
  } catch {
    /* the choice just doesn't outlive the tab */
  }
}

/** How many previous videos show at first, and how many each "Show earlier" adds. */
const PREVIOUS_ROWS = 2;
const PREVIOUS_STEP = 10;

/** A video taken out of the list, held at its old position so Undo can put it back. */
type Removed = { video: VideoSummary; index: number };

/** The backwards half of the panel — everything before the current video in
 *  its context, closest first. `undefined` when the video has no context. */
export type PreviousProps = {
  items: VideoSummary[];
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
};

export function UpNextPanel({
  items,
  title,
  isLoading,
  hasNextPage,
  isFetchingNextPage,
  fetchNextPage,
  previous,
  current,
  ctx,
  autoplay,
  onAutoplay,
  suggestions,
}: {
  items: VideoSummary[];
  title: string;
  /** The context ran out and `items` are similar videos, not the rest of the
   *  list. They get their own heading and never the context's name — a guess
   *  presented as a queue is what made "up next" stop meaning anything. */
  suggestions?: boolean;
  isLoading: boolean;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  previous?: PreviousProps;
  /** The video being watched — the anchor row between Previous and the queue,
   *  so the two halves read as one list. Only shown with a context. */
  current?: VideoSummary;
  ctx?: Record<string, string | undefined>;
  autoplay: boolean;
  onAutoplay: (patch: Partial<Prefs>) => void;
}) {
  const [collapsed, setCollapsed] = useState(loadCollapsed);
  const toggleCollapsed = useCallback(() => {
    setCollapsed((c) => {
      saveCollapsed(!c);
      return !c;
    });
  }, []);

  // Up next never returns a dismissed video (docs/api.md), so dismissing one
  // here has to take it out of the list rather than mark it in place — the
  // same reason a feed grid removes a card. The slot it leaves behind is the
  // only route back, since a dismissed video is not in this list to restore.
  const [removed, setRemoved] = useState<Removed[]>([]);
  const dismiss = useDismissVideo();
  const undismiss = useUndismissVideo();
  // A new video means a new list; anything held for undo belonged to the old one.
  const firstId = items[0]?.id;
  useEffect(() => setRemoved([]), [firstId]);

  const onDismiss = useCallback(
    (video: VideoSummary, index: number) => {
      setRemoved((list) => [...list, { video, index }]);
      dismiss.mutate(video.id, {
        onError: () => setRemoved((list) => list.filter((r) => r.video.id !== video.id)),
      });
    },
    [dismiss],
  );

  const onUndo = useCallback(
    (video: VideoSummary) => {
      setRemoved((list) => list.filter((r) => r.video.id !== video.id));
      undismiss.mutate(video.id);
    },
    [undismiss],
  );

  const removedIds = new Set(removed.map((r) => r.video.id));
  const visible = items.filter((v) => !removedIds.has(v.id));

  // The backwards half is the same list, and a video the viewer went past is
  // as fair a thing to be not interested in as one still to come — so it is
  // dismissed the same way, with its own undo slot held at its old position.
  const [removedPrev, setRemovedPrev] = useState<Removed[]>([]);
  useEffect(() => setRemovedPrev([]), [firstId]);
  const onDismissPrev = useCallback(
    (video: VideoSummary, index: number) => {
      setRemovedPrev((list) => [...list, { video, index }]);
      dismiss.mutate(video.id, {
        onError: () => setRemovedPrev((list) => list.filter((r) => r.video.id !== video.id)),
      });
    },
    [dismiss],
  );
  const onUndoPrev = useCallback(
    (video: VideoSummary) => {
      setRemovedPrev((list) => list.filter((r) => r.video.id !== video.id));
      undismiss.mutate(video.id);
    },
    [undismiss],
  );
  const removedPrevIds = new Set(removedPrev.map((r) => r.video.id));
  const allPrev = previous ? previous.items.filter((v) => !removedPrevIds.has(v.id)) : [];
  // Two rows tall, and "Show earlier" walks further back — the phone's
  // shape. A scroll box inside the sidebar put a scrollbar next to the list
  // and made the page scroll in two places; growing into the page does not.
  const [shownPrev, setShownPrev] = useState(PREVIOUS_ROWS);
  useEffect(() => setShownPrev(PREVIOUS_ROWS), [firstId]);
  const visiblePrev = allPrev.slice(0, shownPrev);
  const moreBefore = allPrev.length > shownPrev || (previous?.hasNextPage ?? false);
  const showEarlier = useCallback(() => {
    setShownPrev((n) => n + PREVIOUS_STEP);
    // Already showing everything loaded: the next page is what "earlier" is.
    if (previous && previous.hasNextPage && !previous.isFetchingNextPage && allPrev.length <= shownPrev + PREVIOUS_STEP) {
      previous.fetchNextPage();
    }
  }, [previous, allPrev.length, shownPrev]);

  if (collapsed) {
    return (
      <aside className="flex w-full flex-none items-center gap-2 px-5 md:w-auto md:flex-col md:px-0">
        <button
          type="button"
          className="btn !px-2.5 !py-2"
          aria-label="Show up next"
          aria-expanded={false}
          title="Show up next"
          onClick={toggleCollapsed}
        >
          <ChevronIcon direction="left" />
        </button>
        <span className="text-[14px] font-extrabold md:hidden">{title}</span>
      </aside>
    );
  }

  return (
    <aside className="flex w-full flex-none flex-col gap-3.5 px-5 md:w-[360px] md:px-0">
      <div className="flex items-baseline justify-between gap-3">
        <span className="text-[16px] font-extrabold">{title}</span>
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 meta text-[12px]">
            Autoplay {autoplay ? "on" : "off"}
            <Toggle on={autoplay} onChange={(on) => onAutoplay({ autoplay: on })} label="Autoplay" />
          </label>
          <button
            type="button"
            className="text-muted-3 hover:text-ink"
            aria-label="Hide up next"
            aria-expanded={true}
            title="Hide up next"
            onClick={toggleCollapsed}
          >
            <ChevronIcon direction="right" />
          </button>
        </div>
      </div>
      {previous && (visiblePrev.length > 0 || removedPrev.length > 0) && (
        /* No heading and no frame: the dimmed rows above the raised anchor
           *are* the previous videos, and the panel reads as one continuous
           list in watch order — the closest predecessor touches the anchor,
           earlier is upward. column-reverse keeps that order and puts "Show
           earlier" at the visual top, where earlier is. */
        <div className="flex flex-col-reverse gap-3.5">
          {slots(visiblePrev, removedPrev).map((slot) =>
            "removed" in slot ? (
              <div key={slot.removed.video.id} className="flex flex-none items-center justify-between gap-3 rounded-[10px] bg-raised-2 px-3 py-2.5">
                <span className="meta text-[12px] line-clamp-1">Hidden from feeds</span>
                <button type="button" className="!text-accent !font-bold text-[12px]" onClick={() => onUndoPrev(slot.removed.video)}>
                  Undo
                </button>
              </div>
            ) : (
              <div key={slot.video.id} className="group flex flex-none items-center gap-3">
                <Link
                  to={watchHref(slot.video, ctx)}
                  className={`flex min-w-0 flex-1 items-center gap-3 text-ink no-underline hover:text-ink ${slot.video.watched ? "opacity-45" : ""}`}
                >
                  <div className="w-32 flex-none">
                    <Thumb video={slot.video} compact className="!rounded-[10px]" />
                  </div>
                  <span className="flex min-w-0 flex-col gap-[3px]">
                    <span className="text-[14px] font-extrabold leading-[1.25] line-clamp-2">{slot.video.title}</span>
                    <span className="meta text-[12px]">
                      {slot.video.channel.name} · {fmtDuration(slot.video.duration)}
                    </span>
                  </span>
                </Link>
                <button
                  type="button"
                  aria-label="Not interested"
                  title="Not interested — hide from feeds"
                  className="flex-none text-muted-3 opacity-100 hover:text-danger focus-visible:opacity-100 md:opacity-0 md:group-hover:opacity-100"
                  onClick={() => onDismissPrev(slot.video, slot.index)}
                >
                  <CloseIcon size={14} />
                </button>
              </div>
            ),
          )}
          {previous.isFetchingNextPage ? (
            <Spinner />
          ) : (
            moreBefore && (
              <button type="button" className="self-start text-[13px] font-bold text-accent" onClick={showEarlier}>
                Show earlier
              </button>
            )
          )}
        </div>
      )}
      {previous && current && (
        <div className="-mx-2 flex items-center gap-3 rounded-[12px] bg-raised px-2 py-2">
          <div className="w-32 flex-none">
            <Thumb video={current} compact className="!rounded-[10px]" />
          </div>
          <span className="flex min-w-0 flex-col gap-[3px]">
            <span className="text-[14px] font-extrabold leading-[1.25] line-clamp-2">{current.title}</span>
            <span className="text-[12px] font-bold text-accent">Now playing</span>
          </span>
        </div>
      )}
      {suggestions && (visible.length > 0 || removed.length > 0) && (
        /* Said before the rows, not after: by the time a viewer has read a
           title they have already taken it for the next episode. */
        <div className="flex flex-col gap-1 pt-1">
          <span className="text-[14px] font-extrabold">Similar videos</span>
          <span className="meta text-[12px]">That was the last one — these are suggestions.</span>
        </div>
      )}
      {isLoading ? (
        <Spinner />
      ) : visible.length === 0 && removed.length === 0 ? (
        <p className="meta">Nothing more in this context.</p>
      ) : (
        slots(visible, removed).map((slot) =>
          "removed" in slot ? (
            <div key={slot.removed.video.id} className="flex items-center justify-between gap-3 rounded-[10px] bg-raised-2 px-3 py-2.5">
              <span className="meta text-[12px] line-clamp-1">Hidden from feeds</span>
              <button type="button" className="!text-accent !font-bold text-[12px]" onClick={() => onUndo(slot.removed.video)}>
                Undo
              </button>
            </div>
          ) : (
            <div key={slot.video.id} className="group flex items-center gap-3">
              <Link
                to={watchHref(slot.video, ctx)}
                className={`flex min-w-0 flex-1 items-center gap-3 text-ink no-underline hover:text-ink ${slot.video.watched ? "opacity-45" : ""}`}
              >
                <div className="w-32 flex-none">
                  <Thumb video={slot.video} compact className="!rounded-[10px]" />
                </div>
                <span className="flex min-w-0 flex-col gap-[3px]">
                  <span className="text-[14px] font-extrabold leading-[1.25] line-clamp-2">{slot.video.title}</span>
                  <span className="meta text-[12px]">
                    {slot.video.channel.name} · {fmtDuration(slot.video.duration)}
                  </span>
                </span>
              </Link>
              {/* Always reachable by keyboard and touch; the pointer version
                  only paints on hover so the list stays quiet to read. */}
              <button
                type="button"
                aria-label="Not interested"
                title="Not interested — hide from feeds"
                className="flex-none text-muted-3 opacity-100 hover:text-danger focus-visible:opacity-100 md:opacity-0 md:group-hover:opacity-100"
                onClick={() => onDismiss(slot.video, slot.index)}
              >
                <CloseIcon size={14} />
              </button>
            </div>
          ),
        )
      )}
      <InfiniteSentinel enabled={hasNextPage && !isFetchingNextPage} onVisible={fetchNextPage} />
      {isFetchingNextPage && (
        <div className="py-3">
          <Spinner />
        </div>
      )}
    </aside>
  );
}

/**
 * The rendered list: the videos still in it, with each undo slot back at the
 * position its video was dismissed from, so the row a viewer just hid does not
 * jump to the end of the list to be undone.
 */
export function slots(
  visible: VideoSummary[],
  removed: Removed[],
): ({ video: VideoSummary; index: number } | { removed: Removed })[] {
  const out: ({ video: VideoSummary; index: number } | { removed: Removed })[] = visible.map((video, index) => ({ video, index }));
  for (const entry of [...removed].sort((a, b) => a.index - b.index)) {
    out.splice(Math.min(entry.index, out.length), 0, { removed: entry });
  }
  return out;
}
