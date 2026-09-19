import { useEffect, useState } from "react";
import { Link } from "react-router";
import type { VideoSummary } from "@/lib/api";
import { useDismissVideo, useSetWatched, useUndismissVideo } from "@/lib/queries";
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

// Shared "Mark seen" / "Mark unseen" behaviour for one video, used by both
// VideoCard and VideoRow so clearing something off an unseen list without
// playing it is one interaction wherever a video appears — the same action
// the phone and the Apple TV put in a card's hold menu.
//
// Unlike "Not interested" this *is* watch state: it goes back to
// TubeArchivist and follows the viewer into every other client
// (docs/design.md, "Player, resume and seen state").
//
// Optimistic like useDismissToggle, and for the same reason: the flip is
// instant, rolls back on error, and resets once `video.watched` itself
// changes — the refetch after invalidation is what confirms it.
export function useWatchedToggle(video: VideoSummary) {
  const setWatched = useSetWatched();
  const [override, setOverride] = useState<boolean | null>(null);
  useEffect(() => setOverride(null), [video.id, video.watched]);
  const watched = override ?? video.watched;
  const toggle = (e: { preventDefault(): void; stopPropagation(): void }) => {
    e.preventDefault();
    e.stopPropagation();
    const next = !watched;
    setOverride(next);
    setWatched.mutate({ id: video.id, watched: next }, { onError: () => setOverride(!next) });
  };
  return { watched, toggle, pending: setWatched.isPending, shown: patchWatched(video, watched) };
}

// The video as the server will report it once the toggle lands, so the check,
// the resume chip and the progress bar follow the optimistic flip instead of
// waiting for the refetch: marking seen keeps the recorded position (a seen
// video is started over, not resumed) and puts progress at 1, marking unseen
// clears both. `last_played_at` is untouched — the server deliberately does
// not bump it, so toggling from a list never reorders history.
function patchWatched(video: VideoSummary, watched: boolean): VideoSummary {
  if (watched === video.watched) return video;
  return { ...video, watched, position: watched ? video.position : 0, progress: watched ? 1 : 0 };
}

// The round toggle on a card and the bare icon on a row are the same action,
// so they share a label: what the click will do, never what the state is.
export function watchedLabel(watched: boolean) {
  return watched ? "Mark unseen" : "Mark seen";
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
        <span className={`absolute flex items-center justify-center rounded-full bg-[rgba(23,24,26,0.8)] text-white ${compact ? "left-2 top-2 h-5 w-5" : "left-3 top-3 h-7 w-7"}`}>
          <CheckIcon size={compact ? 11 : 15} />
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
  canMarkSeen = true,
  onDismiss,
}: {
  video: VideoSummary;
  ctx?: Record<string, string | undefined>;
  showChannel?: boolean;
  /** False inside a music playlist, which records no watch state at all
   *  (docs/api.md, "Music playlists"). */
  canMarkSeen?: boolean;
  /** See useDismissToggle. Pass this from a feed, which never shows a
   *  dismissed video, so the card is pulled out of the list rather than
   *  toggled in place. */
  onDismiss?: (video: VideoSummary) => void;
}) {
  const { dismissed, toggle, pending } = useDismissToggle(video, onDismiss);
  const seen = useWatchedToggle(video);
  return (
    <div className={`flex flex-col gap-2.5 ${seen.watched ? "opacity-50" : ""}`}>
      <div className="relative">
        <Link to={watchHref(video, ctx)} className="block" aria-label={video.title}>
          <Thumb video={seen.shown} />
        </Link>
        {/* 36px circles, and the pseudo-element pushes each hit area out to
            44px so a thumb can land on one without covering the thumbnail
            with a button that large. */}
        <div className="absolute right-2 top-2 flex items-center gap-1.5">
          {canMarkSeen && (
            <button
              type="button"
              aria-label={watchedLabel(seen.watched)}
              title={seen.watched ? "Mark unseen" : "Mark seen — without playing it"}
              onClick={seen.toggle}
              disabled={seen.pending}
              className={`relative flex h-9 w-9 items-center justify-center rounded-full text-white transition-colors before:absolute before:-inset-1 before:content-[''] disabled:opacity-50 ${seen.watched ? "bg-accent" : "bg-[rgba(23,24,26,0.8)] hover:bg-[rgba(23,24,26,0.95)]"}`}
            >
              <CheckIcon size={15} />
            </button>
          )}
          {!dismissed && (
            <button
              type="button"
              aria-label="Not interested"
              title="Not interested — hide from feeds"
              onClick={toggle}
              disabled={pending}
              className="relative flex h-9 w-9 items-center justify-center rounded-full bg-[rgba(23,24,26,0.8)] text-white transition-colors before:absolute before:-inset-1 before:content-[''] hover:bg-[rgba(23,24,26,0.95)] disabled:opacity-50"
            >
              <CloseIcon size={16} />
            </button>
          )}
        </div>
      </div>
      <div className="flex flex-col gap-0.5">
        <Link to={watchHref(video, ctx)} className="text-[16px] font-extrabold leading-[1.25] tracking-[-0.01em] text-ink no-underline hover:text-ink line-clamp-2" title={video.title}>
          {video.title}
        </Link>
        <span className="meta">
          {showChannel && (
            <>
              <Link to={`/channels/${video.channel.id}`}>{video.channel.name}</Link>
              {" · "}
            </>
          )}
          {seen.watched ? seenLabel(video.last_played_at) : `${ccLabel(video.subtitle_langs, video.has_auto_subtitles)} · ${relativeDay(video.published)}`}
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
