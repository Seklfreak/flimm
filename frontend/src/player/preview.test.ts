import { act, renderHook } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { parsePreviewTrack, tileAt, usePreviewTiles } from "./preview";

const track = `WEBVTT

00:00:00.000 --> 00:00:02.000
sheet.jpg#xywh=0,0,160,90

00:00:02.000 --> 00:00:04.000
sheet.jpg#xywh=160,0,160,90

00:00:04.000 --> 00:00:06.000
sheet.jpg#xywh=0,90,160,90
`;

const trackURL = "/media/preview/vid1/preview.vtt";

describe("parsePreviewTrack", () => {
  it("reads each cue as a rectangle of the sheet", () => {
    const tiles = parsePreviewTrack(track, trackURL);
    expect(tiles).toHaveLength(3);
    expect(tiles[0]).toMatchObject({ start: 0, end: 2, x: 0, y: 0, w: 160, h: 90 });
    expect(tiles[2]).toMatchObject({ start: 4, end: 6, x: 0, y: 90 });
    // The sheet is addressed relative to the track it came from, not to the
    // page — the player is at /watch/<id>, the sheet is not.
    expect(tiles[0].url).toBe(new URL("/media/preview/vid1/sheet.jpg", window.location.href).toString());
  });

  it("skips anything malformed rather than throwing", () => {
    const tiles = parsePreviewTrack(
      `WEBVTT

not a cue at all

00:00:00.000 --> 00:00:02.000
sheet.jpg

00:00:02.000 --> 00:00:04.000
sheet.jpg#xywh=1,2,0,90

00:00:04.000 --> 00:00:06.000
sheet.jpg#xywh=0,90,160,90
`,
      trackURL,
    );
    expect(tiles).toHaveLength(1);
    expect(tiles[0].start).toBe(4);
  });

  it("is empty for a track that is not one", () => {
    expect(parsePreviewTrack("", trackURL)).toEqual([]);
    expect(parsePreviewTrack("<html>404</html>", trackURL)).toEqual([]);
  });

  it("reads hours", () => {
    const tiles = parsePreviewTrack(
      "WEBVTT\n\n01:02:03.500 --> 01:02:05.500\nsheet.jpg#xywh=0,0,160,90\n",
      trackURL,
    );
    expect(tiles[0].start).toBeCloseTo(3723.5);
  });
});

describe("tileAt", () => {
  const tiles = parsePreviewTrack(track, trackURL);

  it("finds the tile covering a moment", () => {
    expect(tileAt(tiles, 0)?.x).toBe(0);
    expect(tileAt(tiles, 2.5)?.x).toBe(160);
    expect(tileAt(tiles, 5.9)?.y).toBe(90);
  });

  it("holds the last tile past the end rather than going blank", () => {
    expect(tileAt(tiles, 99)?.start).toBe(4);
  });

  it("has nothing to show without a track", () => {
    expect(tileAt([], 5)).toBeUndefined();
  });
});

describe("usePreviewTiles", () => {
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });

  // The sheet is a full decode of the file and can queue behind other work on
  // the server. Giving up while the player is still open meant a preview that
  // arrived a minute late reached nobody.
  it("keeps asking until the derivation lands", async () => {
    vi.useFakeTimers();
    const notReady = { ok: false, json: async () => ({ error: "preview not ready", state: "running" }) };
    const fetchMock = vi.fn().mockResolvedValue(notReady);
    vi.stubGlobal("fetch", fetchMock);
    const { result } = renderHook(() => usePreviewTiles(trackURL, true));

    // Past the last of the growing gaps, with nothing but 404s so far.
    for (let i = 0; i < 8; i++) await act(async () => void (await vi.advanceTimersByTimeAsync(60_000)));
    expect(fetchMock.mock.calls.length).toBeGreaterThan(4);
    expect(result.current.tiles).toEqual([]);
    // The 404 says which of "still working" and "gave up" this is.
    expect(result.current.state).toBe("running");
    expect(result.current.asked).toBe(fetchMock.mock.calls.length);

    fetchMock.mockResolvedValue({ ok: true, text: async () => track });
    await act(async () => void (await vi.advanceTimersByTimeAsync(60_000)));
    expect(result.current.tiles).toHaveLength(3);
    expect(result.current.state).toBe("done");

    // And then it stops: the answer is on disk and immutable.
    const settled = fetchMock.mock.calls.length;
    await act(async () => void (await vi.advanceTimersByTimeAsync(300_000)));
    expect(fetchMock.mock.calls.length).toBe(settled);
  });
});
