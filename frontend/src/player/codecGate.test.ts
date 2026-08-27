import { afterEach, describe, expect, it, vi } from "vitest";
import type { HLSVariant, StreamInfo, Video } from "@/lib/api";
import { videoDetail } from "@/test/helpers";
import {
  FALLBACK_SCREEN_HEIGHT,
  QUALITY_STORAGE_KEY,
  archivePlays,
  codecFamily,
  codecLabel,
  decide,
  detectCapabilities,
  loadQuality,
  parseQuality,
  pickVariant,
  qualityLabel,
  saveQuality,
  supportsMime,
  supportsNativeHLS,
  type DeviceCapabilities,
} from "./codecGate";

// ---- fixtures ---------------------------------------------------------------

function caps(over: Partial<DeviceCapabilities> = {}): DeviceCapabilities {
  return {
    screenHeight: 1080,
    decodes: { h264: true, hevc: true, av1: false, vp9: false, vp8: false },
    nativeHLS: false,
    ...over,
  };
}

const stream = (codec: string, height = 1080, type: "video" | "audio" = "video"): StreamInfo => ({
  type,
  codec,
  width: (height * 16) / 9,
  height: type === "video" ? height : 0,
  bitrate: 4_500_000,
});

const rung = (height: number, over: Partial<HLSVariant> = {}): HLSVariant => ({
  height,
  url: `/media/hls/vid1/${height}/index.m3u8`,
  state: "pending",
  codec: height > 1080 ? "hevc" : "h264",
  hls_progress: 0,
  ...over,
});

/** A 4K AV1 archive with the full ladder — the case the feature exists for. */
function av1Video(over: Partial<Video> = {}): Video {
  return videoDetail({
    height: 2160,
    streams: [stream("av01", 2160), stream("opus", 0, "audio")],
    hls_url: "/media/hls/vid1/1080/index.m3u8",
    hls_state: "pending",
    hls_variants: [rung(2160), rung(1440), rung(1080), rung(720), rung(480)],
    ...over,
  });
}

// ---- codec names ------------------------------------------------------------

describe("codecFamily", () => {
  it("maps TubeArchivist's short names", () => {
    expect(codecFamily("avc1")).toBe("h264");
    expect(codecFamily("avc1.64001f")).toBe("h264");
    expect(codecFamily("avc3")).toBe("h264");
    expect(codecFamily("vp09")).toBe("vp9");
    expect(codecFamily("vp9")).toBe("vp9");
    expect(codecFamily("av01")).toBe("av1");
    expect(codecFamily("av1")).toBe("av1");
  });

  it("maps the ladder's names, and both HEVC sample entries", () => {
    expect(codecFamily("h264")).toBe("h264");
    expect(codecFamily("hevc")).toBe("hevc");
    expect(codecFamily("hvc1")).toBe("hevc");
    expect(codecFamily("hev1")).toBe("hevc");
  });

  it("calls an unknown codec unknown rather than guessing", () => {
    expect(codecFamily("theora")).toBe("unknown");
    expect(codecFamily("")).toBe("unknown");
  });

  it("labels codecs the way the menu names them", () => {
    expect(codecLabel("av01")).toBe("AV1");
    expect(codecLabel("hvc1")).toBe("HEVC");
    expect(codecLabel("avc1")).toBe("H.264");
  });
});

// ---- capability probes ------------------------------------------------------

describe("supportsMime", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("takes MediaSource's answer when it says yes", () => {
    vi.stubGlobal("MediaSource", { isTypeSupported: (t: string) => t.includes("av01") });
    expect(supportsMime('video/mp4; codecs="av01.0.08M.08"')).toBe(true);
    expect(supportsMime('video/mp4; codecs="vp09.00.10.08"')).toBe(false);
  });

  it("falls back to canPlayType, which is how Safari admits to a native decoder", () => {
    vi.stubGlobal("MediaSource", undefined);
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockImplementation((t: string) =>
      t.includes("hvc1") ? "probably" : "",
    );
    expect(supportsMime('video/mp4; codecs="hvc1.1.6.L93.B0"')).toBe(true);
    expect(supportsMime('video/mp4; codecs="av01.0.08M.08"')).toBe(false);
  });

  it("survives an implementation that throws", () => {
    vi.stubGlobal("MediaSource", {
      isTypeSupported: () => {
        throw new Error("nope");
      },
    });
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
    expect(supportsMime("video/mp4")).toBe(false);
  });

  it("detects native HLS the way Safari answers it", () => {
    vi.stubGlobal("MediaSource", undefined);
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockImplementation((t: string) =>
      t.includes("mpegurl") ? "maybe" : "",
    );
    expect(supportsNativeHLS()).toBe(true);
  });
});

describe("detectCapabilities", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("measures the screen in device pixels", () => {
    vi.stubGlobal("devicePixelRatio", 2);
    vi.spyOn(window.screen, "height", "get").mockReturnValue(1080);
    expect(detectCapabilities().screenHeight).toBe(2160);
  });

  it("assumes 1080p when there is no screen to measure", () => {
    vi.stubGlobal("devicePixelRatio", 1);
    vi.spyOn(window.screen, "height", "get").mockReturnValue(0);
    expect(detectCapabilities().screenHeight).toBe(FALLBACK_SCREEN_HEIGHT);
  });

  it("treats an environment that answers nothing as permissive, not as a codec wall", () => {
    // jsdom's canPlayType returns "" for everything and there is no
    // MediaSource — which must not read as "this browser plays nothing".
    vi.stubGlobal("MediaSource", undefined);
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
    const c = detectCapabilities();
    expect(c.decodes).toEqual({ h264: true, hevc: true, av1: true, vp9: true, vp8: true });
  });

  it("reports only what the probes confirm once they answer", () => {
    vi.stubGlobal("MediaSource", {
      isTypeSupported: (t: string) => t.includes("avc1") || t.includes("vp09") || t.includes("vp9"),
    });
    vi.spyOn(HTMLMediaElement.prototype, "canPlayType").mockReturnValue("");
    const c = detectCapabilities();
    expect(c.decodes.h264).toBe(true);
    expect(c.decodes.vp9).toBe(true);
    expect(c.decodes.av1).toBe(false);
    expect(c.decodes.hevc).toBe(false);
  });
});

// ---- the ladder -------------------------------------------------------------

describe("pickVariant", () => {
  const ladder = [rung(2160), rung(1440), rung(1080), rung(720), rung(480)];

  it("auto takes the tallest rung the screen can show", () => {
    expect(pickVariant("auto", ladder, caps({ screenHeight: 2160 }))?.height).toBe(2160);
    expect(pickVariant("auto", ladder, caps({ screenHeight: 1440 }))?.height).toBe(1440);
    expect(pickVariant("auto", ladder, caps({ screenHeight: 1200 }))?.height).toBe(1080);
  });

  it("drops the HEVC rungs on a browser without an HEVC decoder", () => {
    const noHevc = caps({ screenHeight: 2160, decodes: { h264: true, hevc: false, av1: false, vp9: false, vp8: false } });
    expect(pickVariant("auto", ladder, noHevc)?.height).toBe(1080);
    expect(pickVariant(2160, ladder, noHevc)?.height).toBe(1080);
  });

  it("an explicit height takes that rung, or the nearest lower one offered", () => {
    expect(pickVariant(720, ladder, caps())?.height).toBe(720);
    expect(pickVariant(1440, [rung(1080), rung(720)], caps())?.height).toBe(1080);
  });

  it("falls to the smallest rung rather than to nothing", () => {
    expect(pickVariant(480, [rung(2160), rung(1440)], caps({ screenHeight: 2160 }))?.height).toBe(1440);
    expect(pickVariant("auto", [rung(2160)], caps({ screenHeight: 800 }))?.height).toBe(2160);
  });

  it("is null when there is no ladder, or none of it decodes here", () => {
    expect(pickVariant("auto", undefined, caps())).toBeNull();
    expect(pickVariant("auto", [], caps())).toBeNull();
    const noHevc = caps({ decodes: { h264: true, hevc: false, av1: false, vp9: false, vp8: false } });
    expect(pickVariant("auto", [rung(2160), rung(1440)], noHevc)).toBeNull();
  });

  it("keeps a rung in an unrecognised codec rather than hiding it", () => {
    expect(pickVariant("auto", [rung(1080, { codec: "vvc" })], caps())?.height).toBe(1080);
  });
});

// ---- the decision -----------------------------------------------------------

describe("archivePlays", () => {
  it("is true for a video the server reports no streams for — unknown is not unplayable", () => {
    expect(archivePlays(videoDetail(), caps())).toBe(true);
    expect(archivePlays(videoDetail({ streams: [] }), caps())).toBe(true);
    expect(archivePlays(videoDetail({ streams: [stream("opus", 0, "audio")] }), caps())).toBe(true);
  });

  it("follows the browser's decoders for the video track", () => {
    expect(archivePlays(av1Video(), caps())).toBe(false);
    expect(archivePlays(av1Video(), caps({ decodes: { ...caps().decodes, av1: true } }))).toBe(true);
  });
});

describe("decide", () => {
  it("plays the archive on auto when the browser decodes it — no transcode", () => {
    const v = videoDetail({ streams: [stream("avc1")], hls_variants: [rung(1080), rung(720)] });
    expect(decide(v, "auto", false, caps())).toEqual({ kind: "native", url: v.media_url });
  });

  it("plays a rendition on auto when the archive does not decode here", () => {
    const d = decide(av1Video(), "auto", false, caps({ screenHeight: 1080 }));
    expect(d.kind).toBe("hls");
    if (d.kind !== "hls") return;
    expect(d.variant?.height).toBe(1080);
    expect(d.url).toBe("/media/hls/vid1/1080/index.m3u8");
  });

  it("caps auto at the screen, not at the source", () => {
    const d = decide(av1Video(), "auto", false, caps({ screenHeight: 900 }));
    expect(d.kind === "hls" && d.variant?.height).toBe(720);
  });

  it("honours an explicit height even when the archive would have played", () => {
    const v = videoDetail({ streams: [stream("avc1", 2160)], height: 2160, hls_variants: [rung(1080), rung(720)] });
    const d = decide(v, 720, false, caps());
    expect(d.kind === "hls" && d.variant?.height).toBe(720);
  });

  it("skips the transcode when an explicit pick is at or above the source height", () => {
    const v = videoDetail({ streams: [stream("avc1", 1080)], hls_variants: [rung(1080), rung(720)] });
    expect(decide(v, 1080, false, caps()).kind).toBe("native");
    expect(decide(v, 2160, false, caps()).kind).toBe("native");
  });

  it("audio-only never touches the video track", () => {
    const d = decide(av1Video(), "auto", true, caps());
    expect(d).toEqual({ kind: "native", url: "/media/audio/vid1.webm" });
  });

  it("plays the archive when the server reports no streams at all", () => {
    expect(decide(videoDetail(), "auto", false, caps()).kind).toBe("native");
  });

  it("falls back to hls_url on a server with no ladder", () => {
    const v = videoDetail({
      streams: [stream("vp09")],
      hls_url: "/media/hls/vid1/index.m3u8",
      hls_state: "pending",
    });
    const d = decide(v, "auto", false, caps());
    expect(d).toEqual({ kind: "hls", url: "/media/hls/vid1/index.m3u8", variant: null });
  });

  it("prefers a playable archive over a ladder this browser cannot decode", () => {
    const v = videoDetail({ streams: [stream("avc1", 2160)], hls_variants: [rung(2160), rung(1440)] });
    const noHevc = caps({ decodes: { h264: true, hevc: false, av1: false, vp9: false, vp8: false } });
    expect(decide(v, 1440, false, noHevc).kind).toBe("native");
  });

  it("shows the codec wall only when nothing decodes and there is no rendition", () => {
    const v = videoDetail({ streams: [stream("av01")] });
    const d = decide(v, "auto", false, caps());
    expect(d).toEqual({ kind: "audioOnly", issue: { videoCodec: "av01", audioAvailable: true } });
  });

  it("is unplayable when even the audio rendition is missing", () => {
    const v = videoDetail({ streams: [stream("av01")], audio_url: "" });
    expect(decide(v, "auto", false, caps()).kind).toBe("unplayable");
  });
});

// ---- the stored preference --------------------------------------------------

describe("quality preference", () => {
  it("parses what localStorage holds", () => {
    expect(parseQuality("auto")).toBe("auto");
    expect(parseQuality("1080")).toBe(1080);
    expect(parseQuality(null)).toBe("auto");
    expect(parseQuality("")).toBe("auto");
    expect(parseQuality("nonsense")).toBe("auto");
    expect(parseQuality("-5")).toBe("auto");
  });

  it("round-trips through localStorage", () => {
    saveQuality(720);
    expect(window.localStorage.getItem(QUALITY_STORAGE_KEY)).toBe("720");
    expect(loadQuality()).toBe(720);
    saveQuality("auto");
    expect(loadQuality()).toBe("auto");
  });

  it("labels the current choice", () => {
    expect(qualityLabel("auto")).toBe("Auto");
    expect(qualityLabel(1080)).toBe("1080p");
  });
});
