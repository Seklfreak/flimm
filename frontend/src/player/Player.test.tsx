import { describe, expect, it } from "vitest";
import { fmtSpeed, pickTrack, trackLabel } from "./Player";
import type { SubtitleTrack } from "@/lib/api";

const tracks: SubtitleTrack[] = [
  { lang: "en", source: "auto", url: "/a" },
  { lang: "en", source: "user", url: "/b" },
  { lang: "de", source: "auto", url: "/c" },
];

describe("subtitle picking", () => {
  it("prefers archived tracks over auto for the same language", () => {
    expect(pickTrack(tracks, "en")?.url).toBe("/b");
    expect(pickTrack(tracks, "de")?.url).toBe("/c");
    expect(pickTrack(tracks, "fr")).toBeNull();
    expect(pickTrack(tracks, null)).toBeNull();
  });
  it("labels auto tracks", () => {
    expect(trackLabel(tracks[0])).toBe("English (auto)");
    expect(trackLabel(tracks[1])).toBe("English");
  });
  it("formats speeds", () => {
    expect(fmtSpeed(1)).toBe("1×");
    expect(fmtSpeed(1.25)).toBe("1.25×");
    expect(fmtSpeed(1.5)).toBe("1.5×");
  });
});
