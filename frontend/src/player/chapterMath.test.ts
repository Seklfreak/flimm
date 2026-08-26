import { describe, expect, it } from "vitest";
import {
  chapterMarkerPercents,
  currentChapterIndex,
  nextChapterStart,
  prevChapterStart,
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

describe("sponsorCategoryLabel", () => {
  it("maps known categories and humanizes unknown ones", () => {
    expect(sponsorCategoryLabel("sponsor")).toBe("Sponsor");
    expect(sponsorCategoryLabel("music_offtopic")).toBe("Non-music section");
    expect(sponsorCategoryLabel("some_new_thing")).toBe("some new thing");
  });
});
