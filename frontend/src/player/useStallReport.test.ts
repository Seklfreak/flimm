import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";
import { useStallReport } from "./useStallReport";

// A jsdom <video> reports itself as paused forever, and this hook deliberately
// ignores a paused player — so the flag is the one thing worth faking.
function fakeVideo(paused = false) {
  const el = document.createElement("video");
  Object.defineProperty(el, "paused", { get: () => paused, configurable: true });
  Object.defineProperty(el, "currentTime", { value: 2472.5, writable: true, configurable: true });
  return el;
}

function stallRequests() {
  return (globalThis.fetch as ReturnType<typeof vi.fn>).mock.calls.filter((c) =>
    String(c[0]).endsWith("/stall"),
  );
}

const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("useStallReport", () => {
  beforeEach(() => {
    globalThis.fetch = vi.fn(async () => new Response(null, { status: 204 }));
  });
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("reports a stall when the picture comes back, with what was playing", async () => {
    const el = fakeVideo();
    renderHook(() => useStallReport(el, "yt-id", 1080));

    el.dispatchEvent(new Event("waiting"));
    expect(stallRequests()).toHaveLength(0); // a stall in progress says nothing yet
    await wait(500);
    el.dispatchEvent(new Event("playing"));

    const calls = stallRequests();
    expect(calls).toHaveLength(1);
    expect(String(calls[0][0])).toBe("/api/v1/videos/yt-id/stall");
    const body = JSON.parse(String((calls[0][1] as RequestInit).body));
    expect(body.position).toBe(2472.5);
    expect(body.height).toBe(1080);
    expect(body.client).toBe("web");
    // Measured, not echoed: the server applies its floor to this number.
    expect(body.seconds).toBeGreaterThanOrEqual(0.4);
  });

  // Every player has sub-second gaps between segments. Reporting them would
  // bury the ones a person noticed.
  it("ignores a gap under the floor", async () => {
    const el = fakeVideo();
    renderHook(() => useStallReport(el, "yt-id", 1080));

    el.dispatchEvent(new Event("waiting"));
    await wait(50);
    el.dispatchEvent(new Event("playing"));

    expect(stallRequests()).toHaveLength(0);
  });

  // A stall that was still running when playback stopped has no length: the
  // viewer may simply have left.
  it("abandons a stall that never ended", async () => {
    const el = fakeVideo();
    renderHook(() => useStallReport(el, "yt-id", 0));

    el.dispatchEvent(new Event("waiting"));
    await wait(500);
    el.dispatchEvent(new Event("pause"));
    el.dispatchEvent(new Event("playing"));

    expect(stallRequests()).toHaveLength(0);
  });

  // Waiting for a seek to land is the viewer asking to wait, not the stream
  // failing them.
  it("does not blame a seek", async () => {
    const el = fakeVideo();
    renderHook(() => useStallReport(el, "yt-id", 720));

    el.dispatchEvent(new Event("waiting"));
    await wait(500);
    el.dispatchEvent(new Event("seeking"));
    el.dispatchEvent(new Event("playing"));

    expect(stallRequests()).toHaveLength(0);
  });

  // Nothing to notice yet: a player that has not been started is not stalling.
  it("says nothing while the player is paused", async () => {
    const el = fakeVideo(true);
    renderHook(() => useStallReport(el, "yt-id", 720));

    el.dispatchEvent(new Event("waiting"));
    await wait(500);
    el.dispatchEvent(new Event("playing"));

    expect(stallRequests()).toHaveLength(0);
  });
});
