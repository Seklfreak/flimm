import { useEffect, useState } from "react";
import { Link } from "react-router";
import type { VideoSummary } from "@/lib/api";
import { useDismissVideo, useUndismissVideo } from "@/lib/queries";
import { ccLabel, fmtDuration, relativeDay, seenLabel } from "@/lib/format";
import { CheckIcon, CloseIcon, MediaImg, ProgressBar } from "./ui";

// Shared dismiss/restore behaviour for one video, used by both VideoCard and
// VideoRow so "Not interested" is one interaction wherever a video appears
// (docs/api.md "dismissed"). Optimistic: flips instantly and rolls back on
// error; the override resets once `video.dismissed` itself changes, which is
// what makes it "correct" rather than just locally faked forever — a refetch
// after invalidation is what ultimately confirms it.
//
// A caller that shows videos a feed would never return (FeedPage's grid) must
// pass `onDismiss`: a dismissed video has to disappear from *that* list, not
// just flip a flag in place, so the caller owns removing it (and firing the
// mutation itself, so it can roll its own removal back on failure). Every
// other list — channel, playlist, search, history — keeps the video and just
// needs the in-place toggle, so it omits `onDismiss` and gets the default
// mutate-here behaviour.
export function useDismissToggle(video: VideoSummary, onDismiss?: (video: VideoSummary) => void) {
  const dismiss = useDismissVideo();
  const undismiss = useUndismissVideo();
  const [override, setOverride] = useState<boolean | null>(null);
  useEffect(() => setOverride(null), [video.id, video.dismissed]);
  const dismissed = override ?? video.dismissed;
  const pending = dismiss.isPending || undismiss.isPending;
  const toggle = (e: { preventDefault(): void; stopPropagation(): void }) => {
    e.preventDefault();
    e.stopPropagation();
    if (dismissed) {
      setOverride(false);
      undismiss.mutate(video.id, { onError: () => setOverride(true) });
    } else if (onDismiss) {
      onDismiss(video);
    } else {
      setOverride(true);
      dismiss.mutate(video.id, { onError: () => setOverride(false) });
    }
  };
  return { dismissed, toggle, pending };
}

export function watchHref(v: { id: string }, ctx?: Record<string, string | undefined>) {
  const p = new URLSearchParams();
  for (const [k, val] of Object.entries(ctx ?? {})) if (val) p.set(k, val);
  const s = p.toString();
  return `/watch/${v.id}${s ? `?${s}` : ""}`;
}

// 16:9 thumbnail with the overlays from the design: Resume pill (top-left),
// seen check, duration (bottom-right), progress bar.
export function Thumb({
  video,
  className = "",
  compact = false,
}: {
  video: VideoSummary;
  className?: string;
  compact?: boolean;
}) {
  const inProgress = !video.watched && video.position > 0;
  return (
    <div className={`relative aspect-video overflow-hidden rounded-2xl bg-thumb ${className}`}>
      {video.thumb_url && <MediaImg src={video.thumb_url} alt="" className="absolute inset-0 h-full w-full object-cover" />}
      {!compact && inProgress && <span className="pill left-3 top-3">Resume · {fmtDuration(video.position)}</span>}
      {video.watched && (
        <span className={`absolute flex items-center justify-center rounded-full bg-[rgba(23,24,26,0.8)] text-white ${compact ? "left-2 top-2 h-5 w-5" : "left-3 top-3 h-6 w-6"}`}>
          <CheckIcon size={compact ? 11 : 13} />
        </span>
      )}
      <span className={`pill ${compact ? "bottom-1.5 right-1.5 !px-1.5 !py-0.5 !text-[10px]" : "bottom-3 right-3"}`}>{fmtDuration(video.duration)}</span>
      {inProgress && (
        <ProgressBar value={video.progress} className={`absolute ${compact ? "inset-x-1.5 bottom-1.5 !h-[3px]" : "inset-x-3 bottom-3"}`} />
      )}
    </div>
  );
}

// Grid card (Main / Channel / Feed screens).
export function VideoCard({
  video,
  ctx,
  showChannel = true,
  onDismiss,
}: {
  video: VideoSummary;
  ctx?: Record<string, string | undefined>;
  showChannel?: boolean;
  /** See useDismissToggle. Pass this from a feed, which never shows a
   *  dismissed video, so the card is pulled out of the list rather than
   *  toggled in place. */
  onDismiss?: (video: VideoSummary) => void;
}) {
  const { dismissed, toggle, pending } = useDismissToggle(video, onDismiss);
  return (
    <div className={`flex flex-col gap-2.5 ${video.watched ? "opacity-50" : ""}`}>
      <div className="relative">
        <Link to={watchHref(video, ctx)} className="block" aria-label={video.title}>
          <Thumb video={video} />
        </Link>
        {!dismissed && (
          <button
            type="button"
            aria-label="Not interested"
            title="Not interested — hide from feeds"
            onClick={toggle}
            disabled={pending}
            className="absolute right-2.5 top-2.5 flex h-6 w-6 items-center justify-center rounded-full bg-[rgba(23,24,26,0.8)] text-white transition-colors hover:bg-[rgba(23,24,26,0.95)] disabled:opacity-50"
          >
            <CloseIcon size={12} />
          </button>
        )}
      </div>
      <div className="flex flex-col gap-0.5">
        <Link to={watchHref(video, ctx)} className="text-[16px] font-extrabold leading-[1.25] tracking-[-0.01em] text-ink no-underline hover:text-ink line-clamp-2">
          {video.title}
        </Link>
        <span className="meta">
          {showChannel && (
            <>
              <Link to={`/channels/${video.channel.id}`}>{video.channel.name}</Link>
              {" · "}
            </>
          )}
          {video.watched ? seenLabel(video.last_played_at) : `${ccLabel(video.subtitle_langs, video.has_auto_subtitles)} · ${relativeDay(video.published)}`}
        </span>
        {dismissed && (
          <span className="meta">
            Hidden from feeds ·{" "}
            <button type="button" className="!text-accent !font-bold" onClick={toggle} disabled={pending}>
              Restore
            </button>
          </span>
        )}
      </div>
    </div>
  );
}

// Placeholder that holds a dismissed card's grid slot until the feed refetches
// without it (feeds drop dismissed videos server-side, docs/api.md
// "dismissed"), so the layout doesn't jump and Undo is right where the card
// was — no toast, no navigating elsewhere.
export function DismissedCard({ video, onUndo }: { video: VideoSummary; onUndo: () => void }) {
  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex aspect-video flex-col items-center justify-center gap-2 rounded-2xl bg-thumb text-center">
        <span className="text-[13px] font-bold text-muted">Not interested</span>
        <button type="button" className="btn pri" onClick={onUndo}>
          Undo
        </button>
      </div>
      <div className="flex flex-col gap-0.5">
        <span className="text-[16px] font-extrabold leading-[1.25] tracking-[-0.01em] text-muted line-clamp-1">{video.title}</span>
        <span className="meta">Hidden from feeds</span>
      </div>
    </div>
  );
}

export function VideoGrid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-1 gap-x-6 gap-y-6 sm:grid-cols-2 md:gap-y-7 xl:grid-cols-3">{children}</div>;
}
