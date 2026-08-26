import { describe, expect, it } from "vitest";
import { ccLabel, dayHeading, fmtDuration, fmtDurationLong, relativeDay, remainingUnseen, seenLabel } from "./format";

const now = new Date(2026, 7, 26, 12, 0, 0); // Wed Aug 26 2026
const daysAgo = (n: number) => new Date(now.getTime() - n * 86_400_000).toISOString();

describe("fmtDuration", () => {
  it("formats m:ss and h:mm:ss", () => {
    expect(fmtDuration(561)).toBe("9:21");
    expect(fmtDuration(1476)).toBe("24:36");
    expect(fmtDuration(3723)).toBe("1:02:03");
    expect(fmtDuration(0)).toBe("0:00");
  });
  it("formats long durations", () => {
    expect(fmtDurationLong(15120)).toBe("4 h 12 min");
    expect(fmtDurationLong(2880)).toBe("48 min");
  });
});

describe("relative dates", () => {
  it("relativeDay", () => {
    expect(relativeDay(daysAgo(0), now)).toBe("today");
    expect(relativeDay(daysAgo(1), now)).toBe("yesterday");
    expect(relativeDay(daysAgo(3), now)).toBe("3 days ago");
    expect(relativeDay(daysAgo(8), now)).toBe("last week");
    expect(relativeDay(daysAgo(15), now)).toBe("2 weeks ago");
  });
  it("seenLabel uses weekday within a week", () => {
    expect(seenLabel(daysAgo(3), now)).toBe("seen Sunday");
    expect(seenLabel(daysAgo(1), now)).toBe("seen yesterday");
    expect(seenLabel(null, now)).toBe("seen");
  });
  it("dayHeading groups history", () => {
    expect(dayHeading(daysAgo(0), now)).toBe("Today");
    expect(dayHeading(daysAgo(1), now)).toBe("Yesterday");
    expect(dayHeading(daysAgo(3), now)).toBe("Sunday");
  });
});

describe("ccLabel", () => {
  it("lists archived languages, falls back to auto / none", () => {
    expect(ccLabel(["en"], true)).toBe("CC EN");
    expect(ccLabel(["en", "de"], false)).toBe("CC EN, DE");
    expect(ccLabel([], true)).toBe("CC auto");
    expect(ccLabel([], false)).toBe("no subtitles");
  });
});

describe("remainingUnseen", () => {
  it("subtracts seen from total", () => {
    expect(remainingUnseen(14, 11)).toBe(3);
    expect(remainingUnseen(14, 0)).toBe(14);
  });
  it("clamps at 0 rather than going negative", () => {
    expect(remainingUnseen(14, 14)).toBe(0);
    expect(remainingUnseen(14, 20)).toBe(0);
  });
});
