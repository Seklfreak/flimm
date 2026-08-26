import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { Player, SUBTITLE_OFF, fmtSpeed, pickTrack, trackLabel } from "./Player";
import type { Prefs, SubtitleTrack } from "@/lib/api";
import { mockFetch, renderWithProviders, videoDetail } from "@/test/helpers";

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

const prefs: Prefs = {
  autoplay: true,
  playback_speed: 1,
  subtitle_lang: SUBTITLE_OFF,
  subtitle_size: "medium",
  skip_sponsors: true,
  everything_sort: "newest",
  everything_hide_seen: true,
  everything_include_shorts: false,
  theme: "system",
};

function renderPlayer(audioOnly: boolean) {
  mockFetch({ "GET /api/v1/videos/vid1/chapters": { source: "none", chapters: [] } });
  return renderWithProviders(
    <Player
      video={videoDetail()}
      prefs={prefs}
      audioOnly={audioOnly}
      onToggleAudioOnly={() => {}}
      onPrefs={() => {}}
      onWatched={() => {}}
      onStartOver={async () => {}}
      onEnded={() => {}}
    />,
  );
}

describe("Player audio mode", () => {
  it("plays the video source in video mode", () => {
    const { container } = renderPlayer(false);
    expect(container.querySelector("video")?.getAttribute("src")).toBe("/media/video/vid1.mp4");
  });

  it("plays the audio source in audio mode", () => {
    const { container } = renderPlayer(true);
    expect(container.querySelector("video")?.getAttribute("src")).toBe("/media/audio/vid1.webm");
  });

  it("shows the CC and fullscreen buttons in video mode", () => {
    renderPlayer(false);
    expect(screen.getByLabelText("Subtitles")).toBeTruthy();
    expect(screen.getByLabelText("Fullscreen")).toBeTruthy();
  });

  it("hides the CC and fullscreen buttons in audio mode", () => {
    renderPlayer(true);
    expect(screen.queryByLabelText("Subtitles")).toBeNull();
    expect(screen.queryByLabelText("Fullscreen")).toBeNull();
  });

  it("labels the audio/video toggle for the opposite action", () => {
    renderPlayer(false);
    expect(screen.getByLabelText("Switch to audio only")).toBeTruthy();
  });
});
