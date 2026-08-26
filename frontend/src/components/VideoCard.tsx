import { Link } from "react-router";
import type { VideoSummary } from "@/lib/api";
import { ccLabel, fmtDuration, relativeDay, seenLabel } from "@/lib/format";
import { CheckIcon, MediaImg, ProgressBar } from "./ui";

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
}: {
  video: VideoSummary;
  ctx?: Record<string, string | undefined>;
  showChannel?: boolean;
}) {
  return (
    <div className={`flex flex-col gap-2.5 ${video.watched ? "opacity-50" : ""}`}>
      <Link to={watchHref(video, ctx)} className="block" aria-label={video.title}>
        <Thumb video={video} />
      </Link>
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
      </div>
    </div>
  );
}

export function VideoGrid({ children }: { children: React.ReactNode }) {
  return <div className="grid grid-cols-1 gap-x-6 gap-y-6 sm:grid-cols-2 md:gap-y-7 xl:grid-cols-3">{children}</div>;
}
