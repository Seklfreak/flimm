import { Link } from "react-router";
import type { VideoSummary } from "@/lib/api";
import { CheckIcon, CloseIcon } from "./ui";
import { Thumb, useDismissToggle, useWatchedToggle, watchedLabel, watchHref } from "./VideoCard";

// List row (Playlist / History / Search): 148×84 thumb, title, meta, trailing actions.
// Every row (channel, playlist, search, history) still shows a dismissed
// video — docs/api.md "dismissed" — so, unlike VideoCard in a feed, the row
// never needs to be pulled out of its list: it just toggles in place, which
// is why this never takes an onDismiss override.
export function VideoRow({
  video,
  ctx,
  meta,
  lead,
  actions,
  dim,
  thumbWidth = 148,
  extra,
  canMarkSeen = true,
}: {
  video: VideoSummary;
  ctx?: Record<string, string | undefined>;
  meta: React.ReactNode;
  lead?: React.ReactNode;
  actions?: React.ReactNode;
  dim?: boolean;
  thumbWidth?: number;
  extra?: React.ReactNode;
  /** False inside a music playlist, which records no watch state at all
   *  (docs/api.md, "Music playlists"). */
  canMarkSeen?: boolean;
}) {
  const { dismissed, toggle, pending } = useDismissToggle(video);
  const seen = useWatchedToggle(video);
  return (
    <div className={`row gap-3 md:gap-4 ${dim ? "opacity-55" : ""}`}>
      {lead}
      <Link to={watchHref(video, ctx)} className="flex-none" style={{ width: thumbWidth }} aria-label={video.title}>
        <Thumb video={seen.shown} compact className="!rounded-[10px]" />
      </Link>
      <div className="flex min-w-0 flex-1 flex-col gap-[3px]">
        <Link to={watchHref(video, ctx)} className="text-[15px] font-extrabold leading-[1.25] text-ink no-underline hover:text-ink line-clamp-2" title={video.title}>
          {video.title}
        </Link>
        <span className="meta text-[12px]">{meta}</span>
        {dismissed && (
          <span className="meta text-[12px]">
            Hidden from feeds ·{" "}
            <button type="button" className="!text-accent !font-bold" onClick={toggle} disabled={pending}>
              Restore
            </button>
          </span>
        )}
        {extra}
      </div>
      <div className="flex flex-none items-center gap-2">
        {actions}
        {canMarkSeen && (
          <button
            type="button"
            aria-label={watchedLabel(seen.watched)}
            title={seen.watched ? "Mark unseen" : "Mark seen — without playing it"}
            className={seen.watched ? "text-accent" : "text-muted-3 hover:text-ink"}
            onClick={seen.toggle}
            disabled={seen.pending}
          >
            <CheckIcon size={16} />
          </button>
        )}
        {!dismissed && (
          <button
            type="button"
            aria-label="Not interested"
            title="Not interested — hide from feeds"
            className="text-muted-3 hover:text-danger"
            onClick={toggle}
            disabled={pending}
          >
            <CloseIcon size={16} />
          </button>
        )}
      </div>
    </div>
  );
}
