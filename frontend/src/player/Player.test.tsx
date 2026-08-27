import { afterEach, describe, expect, it, vi } from "vitest";
import { fireEvent, screen } from "@testing-library/react";
import { Player, SUBTITLE_OFF, fmtSpeed, pickTrack, trackLabel, variantHint } from "./Player";
import type { HLSVariant, Prefs, StreamInfo, SubtitleTrack, Video } from "@/lib/api";
import { QUALITY_STORAGE_KEY } from "./codecGate";
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


// ---- renditions and the quality picker --------------------------------------

const stream = (codec: string, height = 1080): StreamInfo => ({
  type: "video",
  codec,
  width: (height * 16) / 9,
  height,
  bitrate: 4_500_000,
});

const rung = (height: number, state: HLSVariant["state"] = "pending"): HLSVariant => ({
  height,
  url: `/media/hls/vid1/${height}/index.m3u8`,
  state,
  codec: height > 1080 ? "hevc" : "h264",
  hls_progress: 0,
});

/** A browser that decodes H.264 and nothing modern — the case the ladder is
 *  for. Without this jsdom answers no probe at all and the gate is permissive. */
function h264OnlyBrowser() {
  vi.stubGlobal("MediaSource", { isTypeSupported: (t: string) => t.includes("avc1") });
  vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
}

function renderVideo(v: Video) {
  mockFetch({
    "GET /api/v1/videos/vid1/chapters": { source: "none", chapters: [] },
    "POST /api/v1/videos/vid1/hls": { state: "running", height: 1080, hls_progress: 0.37 },
  });
  return renderWithProviders(
    <Player
      video={v}
      prefs={prefs}
      audioOnly={false}
      onToggleAudioOnly={() => {}}
      onPrefs={() => {}}
      onWatched={() => {}}
      onStartOver={async () => {}}
      onEnded={() => {}}
    />,
  );
}

describe("quality picker", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    window.localStorage.clear();
  });

  it("is hidden when the server offers no ladder", () => {
    renderVideo(videoDetail());
    expect(screen.queryByLabelText("Video quality")).toBeNull();
  });

  it("lists Auto, what Auto plays, and the rungs this browser can decode", () => {
    h264OnlyBrowser();
    renderVideo(videoDetail({ streams: [stream("avc1")], hls_variants: [rung(2160), rung(1080, "done"), rung(720)] }));
    fireEvent.click(screen.getByLabelText("Video quality"));
    // The control shows the current choice, and the menu repeats it as a row.
    expect(screen.getByLabelText("Video quality").textContent).toBe("Auto");
    expect(screen.getAllByText("Auto")).toHaveLength(2);
    // The archive plays here, so Auto's line names it.
    expect(screen.getByText(/Source · 1080p · H.264/)).toBeTruthy();
    expect(screen.getByText("1080p")).toBeTruthy();
    expect(screen.getByText("720p")).toBeTruthy();
    // 2160p is HEVC, which this browser has no decoder for.
    expect(screen.queryByText(/2160p/)).toBeNull();
    // Each rung's own state, not the video's.
    expect(screen.getByText("ready")).toBeTruthy();
  });

  it("remembers the choice on this device only", () => {
    h264OnlyBrowser();
    renderVideo(videoDetail({ streams: [stream("avc1")], hls_variants: [rung(1080), rung(720)] }));
    fireEvent.click(screen.getByLabelText("Video quality"));
    fireEvent.click(screen.getByText("720p"));
    expect(window.localStorage.getItem(QUALITY_STORAGE_KEY)).toBe("720");
    expect(screen.getByLabelText("Video quality").textContent).toBe("720p");
  });

  it("names each rung's state the way the menu does", () => {
    expect(variantHint("done")).toBe("ready");
    expect(variantHint("running")).toBe("preparing");
    expect(variantHint("pending")).toBe("");
    expect(variantHint("failed")).toBe("failed");
  });
});

describe("codec fallback", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    window.localStorage.clear();
  });

  it("loads a rendition and says so while it is being prepared", () => {
    h264OnlyBrowser();
    renderVideo(videoDetail({ streams: [stream("av01", 2160)], hls_variants: [rung(1080), rung(720)] }));
    expect(screen.getByText(/Preparing a compatible version/)).toBeTruthy();
  });

  it("shows the codec wall only when there is no rendition to fall back to", () => {
    h264OnlyBrowser();
    renderVideo(videoDetail({ streams: [stream("av01")] }));
    expect(screen.getByText(/codec \(av01\) can't be played/)).toBeTruthy();
    expect(screen.getByText("Play audio only")).toBeTruthy();
  });
});
