import { describe, expect, it } from "vitest";
import type { HLSVariant, Loudness } from "@/lib/api";
import type { PreviewTile } from "./preview";
import {
  bufferedAhead,
  describeDelivery,
  describeDroppedFrames,
  describeLoudness,
  describePreview,
  describeRendition,
  describeStream,
  elementState,
  stateWithProgress,
} from "./statsText";

const tile = (over: Partial<PreviewTile> = {}): PreviewTile => ({
  start: 0,
  end: 2,
  url: "/media/preview/v1/sheet.jpg",
  x: 0,
  y: 0,
  w: 160,
  h: 90,
  ...over,
});

const measured: Loudness = {
  state: "done",
  gain_db: -3.9,
  target_lufs: -18,
  measured_lufs: -14.1,
  peak_dbtp: -3.8,
  range_lu: 6.1,
};

describe("describeDelivery", () => {
  // The one line the panel exists for: an encode the server is paying for
  // must not look like a file it is only serving.
  it("separates a direct play from a transcode", () => {
    expect(describeDelivery("native", "archive-decodes", false).kind).toBe("direct");
    expect(describeDelivery("native", "archive-decodes", false).label).toBe("Direct play");
    expect(describeDelivery("hls", "no-decoder", false).kind).toBe("rendition");
    expect(describeDelivery("hls", "no-decoder", false).label).toBe("Transcoded");
  });

  it("calls audio-only what it is, whichever path carried it", () => {
    // Audio-only is served as a plain file, so the kind alone would read as a
    // direct play of the video.
    expect(describeDelivery("native", "audio-only", true).kind).toBe("audio");
  });

  it("has a sentence for every reason the gate can give", () => {
    expect(describeDelivery("hls", "quality-picked", false).why).toMatch(/quality/);
    expect(describeDelivery("unplayable", "nothing-plays", false).why).toMatch(/no decoder/);
  });
});

describe("describeRendition", () => {
  const rung = (over: Partial<HLSVariant> = {}): HLSVariant => ({
    height: 720,
    url: "/media/hls/v1/720/master.m3u8",
    state: "running",
    codec: "h264",
    hls_progress: 0.42,
    ...over,
  });

  it("counts the encoder up while it is still working", () => {
    expect(describeRendition(rung(), "running", 0.42)).toBe("720p · h264 · deriving · 42%");
  });

  // A finished rendition is 100% by definition; printing it invites the reader
  // to mistake it for where playback is.
  it("drops the percentage once it is done", () => {
    expect(describeRendition(rung({ state: "done" }), "done", 1)).toBe("720p · h264 · ready");
  });

  it("falls back to the rung's own state when the poll has not answered", () => {
    expect(describeRendition(rung({ state: "pending" }), null, null)).toBe("720p · h264 · not started");
  });
});

describe("describePreview", () => {
  it("says how fine the grid is once the sheet is there", () => {
    const tiles = [tile(), tile({ start: 2, end: 4 })];
    expect(describePreview({ tiles, state: "done", progress: 1, asked: 3 }, true)).toBe("ready · 2 stills, 160×90, every 2.0s");
  });

  // The whole point: a wait and a failure look identical on the scrubber, and
  // so do a wait and a wedge until the wait carries a number.
  it("tells a wait apart from a failure, and shows the shape of the wait", () => {
    expect(describePreview({ tiles: [], state: "running", progress: 0.42, asked: 6 }, true)).toBe("deriving · 42% · asked 6×");
    expect(describePreview({ tiles: [], state: "failed", progress: 0, asked: 6 }, true)).toBe("failed · asked 6×");
  });

  it("says when there is nothing to wait for", () => {
    expect(describePreview({ tiles: [], state: null, progress: 0, asked: 0 }, false)).toMatch(/not offered/);
    expect(describePreview({ tiles: [], state: null, progress: 0, asked: 0 }, true)).toMatch(/not asked/);
  });
});

describe("stateWithProgress", () => {
  it("counts a running job up", () => {
    expect(stateWithProgress("running", 0.42)).toBe("deriving · 42%");
  });

  // 100% on a finished job says nothing, and 0% on one that has not started
  // reads as a stall — neither is worth the reader's attention.
  it("leaves the number off when it would mean nothing", () => {
    expect(stateWithProgress("done", 1)).toBe("ready");
    expect(stateWithProgress("pending", 0)).toBe("not started");
    expect(stateWithProgress("running", 0)).toBe("deriving");
    expect(stateWithProgress("running", undefined)).toBe("deriving");
    expect(stateWithProgress("failed", 0.5)).toBe("failed");
  });
});

describe("describeLoudness", () => {
  it("reports the gain that actually reached the element", () => {
    expect(describeLoudness(measured, true)).toBe("-3.9 dB · measured -14.1 LUFS, peak -3.8 dBTP");
  });

  // A measurement that exists while the preference is off changes nothing
  // about what you hear, and must not read as if it did.
  it("says nothing is applied when the preference is off", () => {
    expect(describeLoudness(measured, false)).toMatch(/off/);
  });

  it("does not invent a gain from a measurement that has not finished", () => {
    expect(describeLoudness({ ...measured, state: "running", progress: 0.6 }, true)).toBe("deriving · 60%");
    expect(describeLoudness({ ...measured, state: "running" }, true)).toBe("deriving");
    expect(describeLoudness(undefined, true)).toBe("waiting");
  });
});

describe("bufferedAhead", () => {
  const ranges = (spans: [number, number][]): TimeRanges =>
    ({
      length: spans.length,
      start: (i: number) => spans[i][0],
      end: (i: number) => spans[i][1],
    }) as TimeRanges;

  it("measures from the playhead to the end of the range it is in", () => {
    expect(bufferedAhead({ currentTime: 10, buffered: ranges([[0, 25]]) })).toBe(15);
  });

  // Bytes somewhere else on the timeline are not something to play.
  it("ignores a range the playhead is not inside", () => {
    expect(bufferedAhead({ currentTime: 10, buffered: ranges([[60, 120]]) })).toBe(0);
    expect(bufferedAhead({ currentTime: 70, buffered: ranges([[0, 25], [60, 120]]) })).toBe(50);
  });

  it("has nothing to say without an element", () => {
    expect(bufferedAhead(null)).toBeNull();
  });
});

describe("the smaller readings", () => {
  it("prints a stream as far as it is known", () => {
    expect(describeStream(1080, "avc1.640028")).toBe("1080p · avc1.640028");
    expect(describeStream(0, "")).toBe("unknown");
  });

  it("gives dropped frames as a share, and nothing before there are any", () => {
    expect(describeDroppedFrames({ droppedVideoFrames: 12, totalVideoFrames: 3410 })).toBe("12 of 3,410 (0.4%)");
    expect(describeDroppedFrames({ droppedVideoFrames: 0, totalVideoFrames: 0 })).toBeNull();
    expect(describeDroppedFrames(null)).toBeNull();
  });

  it("says the element's states in words", () => {
    expect(elementState(4, 2)).toBe("enough data · loading");
    expect(elementState(0, 3)).toBe("nothing · no source");
  });
});
