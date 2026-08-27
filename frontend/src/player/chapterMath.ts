import type { Chapter, SponsorActionType, SponsorSegment } from "@/lib/api";

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
// the bar and dropping zero/negative-length ranges. Points of interest and
// whole-video labels are not ranges, so they are not tinted.
export function sponsorRangePercents(segments: SponsorSegment[], duration: number): SponsorRange[] {
  if (duration <= 0) return [];
  return segments
    .filter((s) => sponsorAction(s) !== "poi" && sponsorAction(s) !== "full")
    .map((s) => {
      const left = clampPct((s.start / duration) * 100);
      const right = clampPct((s.end / duration) * 100);
      return { category: s.category, leftPct: left, widthPct: Math.max(0, right - left) };
    })
    .filter((r) => r.widthPct > 0);
}

// ---- Actions --------------------------------------------------------------

// A server that predates action types only ever sent skippable segments.
export function sponsorAction(segment: SponsorSegment): SponsorActionType {
  return segment.action_type ?? "skip";
}

// Only these categories are skipped/muted automatically; intro, outro,
// music_offtopic and the rest are tinted on the scrubber but left alone.
// Matches the Apple clients (FlimmKit's SponsorRules).
export const AUTO_SKIP_CATEGORIES = new Set(["sponsor", "selfpromo", "interaction"]);

function acts(segment: SponsorSegment, action: SponsorActionType, time: number, margin: number): boolean {
  return (
    sponsorAction(segment) === action &&
    AUTO_SKIP_CATEGORIES.has(segment.category) &&
    segment.end > segment.start &&
    time >= segment.start &&
    time < segment.end - margin
  );
}

// The segment playback is inside, if it is one that should be skipped. The
// small margin stops a seek landing just before the end from looping.
export function segmentToSkip(segments: SponsorSegment[], time: number): SponsorSegment | undefined {
  return segments.find((s) => acts(s, "skip", time, 0.5));
}

// The segment playback is inside, if it is one that should be muted. The
// contributor marked it "mute" rather than "skip" because the video still
// matters there — only the audio does not.
export function segmentToMute(segments: SponsorSegment[], time: number): SponsorSegment | undefined {
  return segments.find((s) => acts(s, "mute", time, 0));
}

// ---- The highlight --------------------------------------------------------

// How far ahead of playback the highlight has to be for jumping to it to be
// worth offering. Below it the viewer is already there.
export const HIGHLIGHT_LEAD = 1;

// The point of interest a contributor marked as the highlight — "where the
// video actually starts". The earliest one, when a video somehow has several.
export function highlightSegment(segments: SponsorSegment[]): SponsorSegment | undefined {
  return segments
    .filter((s) => sponsorAction(s) === "poi" && s.start >= 0)
    .sort((a, b) => a.start - b.start)[0];
}

// The highlight worth offering at `time`: there is one, and playback has not
// reached it. Nothing here is automatic — a point of interest is never jumped
// to for the viewer, only offered, whatever the skip preference says.
export function highlightToOffer(segments: SponsorSegment[], time: number): SponsorSegment | undefined {
  const highlight = highlightSegment(segments);
  return highlight && time < highlight.start - HIGHLIGHT_LEAD ? highlight : undefined;
}

// Percent-of-duration positions for segments that mark an instant rather than
// a range — the highlight. Drawn as a marker, never as a band.
export function sponsorPointPercents(segments: SponsorSegment[], duration: number): number[] {
  if (duration <= 0) return [];
  return segments.filter((s) => sponsorAction(s) === "poi").map((s) => clampPct((s.start / duration) * 100));
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
