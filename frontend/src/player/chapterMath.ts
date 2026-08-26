import type { Chapter, SponsorSegment } from "@/lib/api";

// ---- Chapter lookup ---------------------------------------------------

// Index of the chapter containing `time`, or -1 when there are no chapters
// or `time` is before the first one.
export function currentChapterIndex(chapters: Chapter[], time: number): number {
  let idx = -1;
  for (let i = 0; i < chapters.length; i++) {
    if (chapters[i].start <= time) idx = i;
    else break;
  }
  return idx;
}

const PREV_CHAPTER_THRESHOLD = 3;

// "]" — start of the next chapter, or null when `time` is already in/past
// the last one.
export function nextChapterStart(chapters: Chapter[], time: number): number | null {
  const next = chapters.find((c) => c.start > time);
  return next ? next.start : null;
}

// "[" — like most players: more than `threshold` seconds into the current
// chapter jumps back to its own start; within that window it jumps to the
// previous chapter's start instead. Null when there's nowhere earlier to go.
export function prevChapterStart(chapters: Chapter[], time: number, threshold = PREV_CHAPTER_THRESHOLD): number | null {
  const idx = currentChapterIndex(chapters, time);
  if (idx === -1) return null;
  const cur = chapters[idx];
  if (time - cur.start > threshold) return cur.start;
  if (idx === 0) return null;
  return chapters[idx - 1].start;
}

// ---- Scrubber marker offsets --------------------------------------------

function clampPct(p: number): number {
  return Math.max(0, Math.min(100, p));
}

// Percent-of-duration offsets for chapter boundary ticks. The first
// chapter always starts at 0, so it's skipped — a tick at the very edge of
// the bar is invisible and not worth drawing.
export function chapterMarkerPercents(chapters: Chapter[], duration: number): number[] {
  if (duration <= 0 || chapters.length < 2) return [];
  return chapters.slice(1).map((c) => clampPct((c.start / duration) * 100));
}

export interface SponsorRange {
  category: string;
  leftPct: number;
  widthPct: number;
}

// Percent-of-duration left/width for each SponsorBlock segment, clamped to
// the bar and dropping zero/negative-length ranges.
export function sponsorRangePercents(segments: SponsorSegment[], duration: number): SponsorRange[] {
  if (duration <= 0) return [];
  return segments
    .map((s) => {
      const left = clampPct((s.start / duration) * 100);
      const right = clampPct((s.end / duration) * 100);
      return { category: s.category, leftPct: left, widthPct: Math.max(0, right - left) };
    })
    .filter((r) => r.widthPct > 0);
}

// ---- Category labels ------------------------------------------------------

const CATEGORY_LABELS: Record<string, string> = {
  sponsor: "Sponsor",
  selfpromo: "Self-promo",
  interaction: "Interaction reminder",
  intro: "Intro",
  outro: "Outro",
  preview: "Preview/recap",
  music_offtopic: "Non-music section",
  filler: "Filler tangent",
  poi_highlight: "Highlight",
  exclusive_access: "Exclusive access",
};

export function sponsorCategoryLabel(category: string): string {
  return CATEGORY_LABELS[category] ?? category.replace(/_/g, " ");
}
