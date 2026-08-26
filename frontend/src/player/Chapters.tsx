import { useState } from "react";
import type { Chapter } from "@/lib/api";
import { fmtDuration } from "@/lib/format";

export interface ChaptersProps {
  chapters: Chapter[];
  activeIndex: number;
  onSeek: (time: number) => void;
}

// Collapsible chapter list under the video. Renders nothing when there are
// no chapters (roughly a third of videos) so it never reserves space or
// pushes "Up next" around.
export function Chapters({ chapters, activeIndex, onSeek }: ChaptersProps) {
  const [open, setOpen] = useState(true);
  if (chapters.length === 0) return null;

  return (
    <div className="rounded-[14px] bg-raised-2">
      <button
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
        onClick={() => setOpen((o) => !o)}
        aria-expanded={open}
      >
        <span className="text-[14px] font-extrabold">Chapters · {chapters.length}</span>
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className={`flex-none text-muted-2 transition-transform ${open ? "rotate-180" : ""}`}
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>
      {open && (
        <div className="flex flex-col gap-0.5 px-2 pb-2">
          {chapters.map((c, i) => (
            <button
              key={`${c.start}-${c.title}`}
              onClick={() => onSeek(c.start)}
              className={`flex items-center gap-3 rounded-lg px-2.5 py-2 text-left text-[13px] font-semibold transition-colors ${
                i === activeIndex ? "bg-accent text-white" : "text-ink-2 hover:bg-raised"
              }`}
            >
              <span className={`tabular-nums text-[12px] font-bold ${i === activeIndex ? "text-white/80" : "text-muted-2"}`}>
                {fmtDuration(c.start)}
              </span>
              <span className="min-w-0 flex-1 truncate">{c.title}</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
