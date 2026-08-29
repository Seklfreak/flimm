import { describe, expect, it } from "vitest";
import {
  chapterMarkerPercents,
  currentChapterIndex,
  nextChapterStart,
  prevChapterStart,
  highlightSegment,
  highlightToOffer,
  segmentToMute,
  segmentToOffer,
  segmentToSkip,
  sponsorPointPercents,
  sponsorAction,
  sponsorCategoryLabel,
  sponsorRangePercents,
} from "./chapterMath";
import type { Chapter } from "@/lib/api";

const chapters: Chapter[] = [
  { start: 0, end: 30, title: "Intro" },
  { start: 30, end: 90, title: "Setup" },
  { start: 90, end: 200, title: "Deep dive" },
];

describe("currentChapterIndex", () => {
  it("returns -1 for an empty list", () => {
    expect(currentChapterIndex([], 10)).toBe(-1);
  });
  it("finds the chapter containing the given time", () => {
    expect(currentChapterIndex(chapters, 0)).toBe(0);
    expect(currentChapterIndex(chapters, 29.9)).toBe(0);
    expect(currentChapterIndex(chapters, 30)).toBe(1);
    expect(currentChapterIndex(chapters, 199)).toBe(2);
  });
});

describe("nextChapterStart", () => {
  it("returns the next boundary", () => {
    expect(nextChapterStart(chapters, 10)).toBe(30);
    expect(nextChapterStart(chapters, 35)).toBe(90);
  });
  it("returns null past the last chapter", () => {
    expect(nextChapterStart(chapters, 150)).toBeNull();
  });
  it("returns null for an empty list", () => {
    expect(nextChapterStart([], 10)).toBeNull();
  });
});

describe("prevChapterStart", () => {
  it("jumps to the current chapter's start when more than 3s in", () => {
    expect(prevChapterStart(chapters, 50)).toBe(30);
  });
  it("jumps to the previous chapter when within 3s of the current chapter's start", () => {
    expect(prevChapterStart(chapters, 31)).toBe(0);
    expect(prevChapterStart(chapters, 32.9)).toBe(0);
  });
  it("returns null near the very start of the first chapter", () => {
    expect(prevChapterStart(chapters, 1)).toBeNull();
    expect(prevChapterStart(chapters, 0)).toBeNull();
  });
  it("returns null for an empty list", () => {
    expect(prevChapterStart([], 10)).toBeNull();
  });
  it("honours a custom threshold", () => {
    expect(prevChapterStart(chapters, 32, 1)).toBe(30);
    expect(prevChapterStart(chapters, 32, 5)).toBe(0);
  });
});

describe("chapterMarkerPercents", () => {
  it("skips the first chapter and computes percents for the rest", () => {
    expect(chapterMarkerPercents(chapters, 200)).toEqual([15, 45]);
  });
  it("returns nothing with no duration, no chapters, or a single chapter", () => {
    expect(chapterMarkerPercents(chapters, 0)).toEqual([]);
    expect(chapterMarkerPercents([], 200)).toEqual([]);
    expect(chapterMarkerPercents([{ start: 0, end: 10, title: "x" }], 10)).toEqual([]);
  });
});

describe("sponsorRangePercents", () => {
  it("computes left/width offsets and drops zero-length ranges", () => {
    const segs = [
      { category: "sponsor", start: 20, end: 40 },
      { category: "outro", start: 190, end: 190 },
    ];
    expect(sponsorRangePercents(segs, 200)).toEqual([{ category: "sponsor", leftPct: 10, widthPct: 10 }]);
  });
  it("returns nothing with no duration", () => {
    expect(sponsorRangePercents([{ category: "sponsor", start: 0, end: 10 }], 0)).toEqual([]);
  });
});

describe("sponsor actions", () => {
  const segs = [
    { category: "sponsor", action_type: "skip" as const, start: 20, end: 40 },
    { category: "selfpromo", action_type: "mute" as const, start: 60, end: 80 },
    { category: "poi_highlight", action_type: "poi" as const, start: 100, end: 100 },
    { category: "outro", action_type: "skip" as const, start: 120, end: 140 },
  ];

  it("treats a segment without an action type as a skip", () => {
    expect(sponsorAction({ category: "sponsor", start: 0, end: 1 })).toBe("skip");
    expect(sponsorAction(segs[1])).toBe("mute");
  });

  // What the viewer set each category to; the server sends one of these for
  // every category it knows.
  const actions = { sponsor: "skip", selfpromo: "skip", outro: "ask" };

  it("skips only skip segments in a category set to skip", () => {
    expect(segmentToSkip(segs, 25, actions)?.category).toBe("sponsor");
    expect(segmentToSkip(segs, 70, actions)).toBeUndefined(); // a mute segment is not skipped
    expect(segmentToSkip(segs, 130, actions)).toBeUndefined(); // outro is "ask": offered, not taken
    expect(segmentToSkip(segs, 39.9, actions)).toBeUndefined(); // inside the end margin
    expect(segmentToSkip(segs, 25, { sponsor: "off" })).toBeUndefined();
    expect(segmentToSkip(segs, 25, {})).toBeUndefined(); // a category nobody set does nothing
  });

  it("offers a category set to ask, rather than taking it", () => {
    expect(segmentToOffer(segs, 130, actions)?.category).toBe("outro");
    expect(segmentToOffer(segs, 25, actions)).toBeUndefined(); // sponsor is skipped outright
    expect(segmentToOffer(segs, 139, actions)).toBeUndefined(); // too late to be worth a button
    expect(segmentToOffer(segs, 130, { outro: "off" })).toBeUndefined();
  });

  it("mutes only mute segments, to their very end, unless the category is off", () => {
    expect(segmentToMute(segs, 60, actions)?.category).toBe("selfpromo");
    expect(segmentToMute(segs, 79.9, actions)?.category).toBe("selfpromo");
    expect(segmentToMute(segs, 80, actions)).toBeUndefined();
    expect(segmentToMute(segs, 25, actions)).toBeUndefined(); // a skip segment is not muted
    expect(segmentToMute(segs, 60, { selfpromo: "ask" })?.category).toBe("selfpromo");
    expect(segmentToMute(segs, 60, { selfpromo: "off" })).toBeUndefined();
  });

  it("does not tint points of interest or whole-video labels", () => {
    const ranges = sponsorRangePercents(
      [
        { category: "sponsor", action_type: "skip", start: 0, end: 100 },
        { category: "poi_highlight", action_type: "poi", start: 50, end: 50 },
        { category: "sponsor", action_type: "full", start: 0, end: 200 },
      ],
      200,
    );
    expect(ranges).toEqual([{ category: "sponsor", leftPct: 0, widthPct: 50 }]);
  });
});

describe("the highlight", () => {
  const withHighlight = [
    { category: "sponsor", action_type: "skip" as const, start: 0, end: 30 },
    { category: "poi_highlight", action_type: "poi" as const, start: 90, end: 90 },
  ];

  it("finds the point of interest, and the earliest one when there are several", () => {
    expect(highlightSegment(withHighlight)?.start).toBe(90);
    expect(
      highlightSegment([
        { category: "poi_highlight", action_type: "poi", start: 120, end: 120 },
        { category: "poi_highlight", action_type: "poi", start: 40, end: 40 },
      ])?.start,
    ).toBe(40);
    expect(highlightSegment([{ category: "sponsor", action_type: "skip", start: 0, end: 30 }])).toBeUndefined();
  });

  it("is offered only while playback is still before it", () => {
    expect(highlightToOffer(withHighlight, 0)?.start).toBe(90);
    expect(highlightToOffer(withHighlight, 89.5)).toBeUndefined(); // inside the lead, already there
    expect(highlightToOffer(withHighlight, 120)).toBeUndefined();
  });

  it("positions it as a marker, not a band", () => {
    expect(sponsorPointPercents(withHighlight, 180)).toEqual([50]);
    expect(sponsorRangePercents(withHighlight, 180).map((r) => r.category)).toEqual(["sponsor"]);
  });
});

describe("sponsorCategoryLabel", () => {
  it("maps known categories and humanizes unknown ones", () => {
    expect(sponsorCategoryLabel("sponsor")).toBe("Sponsor");
    expect(sponsorCategoryLabel("music_offtopic")).toBe("Non-music section");
    expect(sponsorCategoryLabel("some_new_thing")).toBe("some new thing");
  });
});
