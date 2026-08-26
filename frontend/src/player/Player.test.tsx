import { describe, expect, it } from "vitest";
import { SUBTITLE_OFF, fmtSpeed, pickTrack, trackLabel } from "./Player";
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

describe("subtitle defaults", () => {
  const track = (lang: string, source: "user" | "auto") => ({ lang, source, url: `/media/subtitles/x/${lang}.vtt` });

  it("picks English by default, preferring an archived track over an auto one", () => {
    const tracks = [track("en", "auto"), track("en", "user"), track("de", "user")];
    expect(pickTrack(tracks, "en")?.source).toBe("user");
  });

  it("falls back to an auto-generated English track", () => {
    expect(pickTrack([track("en", "auto")], "en")?.lang).toBe("en");
  });

  it("matches a regional variant of the preferred language", () => {
    expect(pickTrack([track("en-US", "user")], "en")?.lang).toBe("en-US");
  });

  it("returns nothing when subtitles are explicitly off", () => {
    expect(pickTrack([track("en", "user")], SUBTITLE_OFF)).toBeNull();
  });

  it("returns nothing when the preferred language has no track", () => {
    expect(pickTrack([track("de", "user")], "en")).toBeNull();
  });
});
