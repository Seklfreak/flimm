import { useState } from "react";
import type { Chapter, SponsorSegment } from "@/lib/api";
import { fmtDuration } from "@/lib/format";
import { chapterMarkerPercents, currentChapterIndex, sponsorCategoryLabel, sponsorPointPercents, sponsorRangePercents } from "./chapterMath";
import { tileAt, type PreviewTile } from "./preview";

export interface ScrubberProps {
  time: number;
  duration: number;
  chapters: Chapter[];
  sponsorblock: SponsorSegment[];
  onSeek: (time: number) => void;
  /** Scrub-preview stills, when the server has derived them; empty otherwise,
   *  and the scrubber simply has no pictures. */
  preview?: PreviewTile[];
}

// Progress bar with chapter tick marks + a muted SponsorBlock tint overlay.
// Decorative children are pointer-events-none so a click always reaches the
// container's seek handler, and hover shows a small tooltip (chapter title,
// or the segment category over a tinted range) following the cursor.
export function Scrubber({ time, duration, chapters, sponsorblock, onSeek, preview = [] }: ScrubberProps) {
  const [hover, setHover] = useState<{ x: number; time: number } | null>(null);

  const pct = duration > 0 ? (time / duration) * 100 : 0;
  const ticks = chapterMarkerPercents(chapters, duration);
  const sponsorRanges = sponsorRangePercents(sponsorblock, duration);
  const sponsorPoints = sponsorPointPercents(sponsorblock, duration);

  const posAt = (e: { clientX: number; currentTarget: HTMLDivElement }) => {
    const r = e.currentTarget.getBoundingClientRect();
    const x = Math.max(0, Math.min(r.width, e.clientX - r.left));
    const frac = r.width > 0 ? x / r.width : 0;
    return { x, t: frac * duration };
  };

  const onMove = (e: React.MouseEvent<HTMLDivElement>) => {
    const { x, t } = posAt(e);
    setHover({ x, time: t });
  };

  const onDown = (e: React.PointerEvent<HTMLDivElement>) => {
    onSeek(posAt(e).t);
  };

  const hoverTile = hover ? tileAt(preview, hover.time) : undefined;
  const hoverSponsor = hover ? sponsorblock.find((s) => hover.time >= s.start && hover.time < s.end) : undefined;
  const hoverChapterIdx = hover && chapters.length > 0 ? currentChapterIndex(chapters, hover.time) : -1;
  const hoverChapter = hoverChapterIdx >= 0 ? chapters[hoverChapterIdx] : undefined;
  const hoverLabel = hoverSponsor
    ? `${sponsorCategoryLabel(hoverSponsor.category)} · ${fmtDuration(hover!.time)}`
    : hoverChapter
      ? `${hoverChapter.title} · ${fmtDuration(hover!.time)}`
      : hover
        ? fmtDuration(hover.time)
        : null;

  return (
    <div
      className="relative h-1 cursor-pointer rounded-sm bg-white/35"
      onPointerDown={onDown}
      onMouseMove={onMove}
      onMouseLeave={() => setHover(null)}
      role="slider"
      aria-valuemin={0}
      aria-valuemax={duration}
      aria-valuenow={time}
      aria-label="Seek"
    >
      {sponsorRanges.map((r, i) => (
        <div
          key={i}
          className="pointer-events-none absolute inset-y-0 rounded-sm bg-sponsor"
          style={{ left: `${r.leftPct}%`, width: `${r.widthPct}%` }}
        />
      ))}
      <div className="pointer-events-none h-full rounded-sm bg-accent" style={{ width: `${pct}%` }} />
      {ticks.map((p, i) => (
        <div key={i} className="pointer-events-none absolute -top-px bottom-[-1px] w-px bg-black/50" style={{ left: `${p}%` }} />
      ))}
      {/* The highlight is an instant, not a band: a diamond on the bar. */}
      {sponsorPoints.map((p, i) => (
        <div
          key={i}
          className="pointer-events-none absolute -top-[3px] h-[7px] w-[7px] -translate-x-1/2 rotate-45 bg-accent"
          style={{ left: `${p}%` }}
        />
      ))}
      <div className="pointer-events-none absolute -top-[5px] h-3.5 w-3.5 -translate-x-1/2 rounded-full bg-white" style={{ left: `${pct}%` }} />
      {/* The still for wherever the cursor is. One sheet is in the browser's
          memory after the first hover, so dragging costs nothing: the tile is
          a background-position, not a fetch. */}
      {hover && hoverTile && (
        <div
          className="pointer-events-none absolute bottom-8 -translate-x-1/2 overflow-hidden rounded-lg border border-white/15 shadow-modal"
          style={{
            left: hover.x,
            width: hoverTile.w,
            height: hoverTile.h,
            backgroundImage: `url(${hoverTile.url})`,
            backgroundPosition: `-${hoverTile.x}px -${hoverTile.y}px`,
          }}
        />
      )}
      {hoverLabel && (
        <div
          className="pointer-events-none absolute bottom-3.5 -translate-x-1/2 whitespace-nowrap rounded-md bg-[rgba(23,24,26,0.92)] px-2 py-1 text-[11px] font-bold text-white"
          style={{ left: hover!.x }}
        >
          {hoverLabel}
        </div>
      )}
    </div>
  );
}
