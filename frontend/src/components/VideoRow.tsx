import { Link } from "react-router";
import type { VideoSummary } from "@/lib/api";
import { Thumb, watchHref } from "./VideoCard";

// List row (Playlist / History / Search): 148×84 thumb, title, meta, trailing actions.
export function VideoRow({
  video,
  ctx,
  meta,
  lead,
  actions,
  dim,
  thumbWidth = 148,
  extra,
}: {
  video: VideoSummary;
  ctx?: Record<string, string | undefined>;
  meta: React.ReactNode;
  lead?: React.ReactNode;
  actions?: React.ReactNode;
  dim?: boolean;
  thumbWidth?: number;
  extra?: React.ReactNode;
}) {
  return (
    <div className={`row gap-3 md:gap-4 ${dim ? "opacity-55" : ""}`}>
      {lead}
      <Link to={watchHref(video, ctx)} className="flex-none" style={{ width: thumbWidth }} aria-label={video.title}>
        <Thumb video={video} compact className="!rounded-[10px]" />
      </Link>
      <div className="flex min-w-0 flex-1 flex-col gap-[3px]">
        <Link to={watchHref(video, ctx)} className="text-[15px] font-extrabold leading-[1.25] text-ink no-underline hover:text-ink line-clamp-2">
          {video.title}
        </Link>
        <span className="meta text-[12px]">{meta}</span>
        {extra}
      </div>
      {actions && <div className="flex flex-none items-center gap-2">{actions}</div>}
    </div>
  );
}
