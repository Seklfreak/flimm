import { describe, expect, it } from "vitest";
import { renderHook } from "@testing-library/react";
import type { SponsorSegment } from "@/lib/api";
import { useSponsorSkip } from "./useSponsorSkip";

const segments: SponsorSegment[] = [
  { category: "sponsor", action_type: "skip", start: 10, end: 20 },
  { category: "selfpromo", action_type: "mute", start: 30, end: 40 },
];

// What the viewer has these categories set to.
const actions = { sponsor: "skip", selfpromo: "skip" };

// A jsdom <video> is enough: the hook only reads currentTime, writes it, and
// toggles muted on timeupdate.
function fakeVideo() {
  const el = document.createElement("video");
  const tick = (t: number) => {
    el.currentTime = t;
    el.dispatchEvent(new Event("timeupdate"));
  };
  return { el, tick };
}

describe("useSponsorSkip", () => {
  it("seeks past a skip segment", () => {
    const { el, tick } = fakeVideo();
    const skipped: SponsorSegment[] = [];
    renderHook(() => useSponsorSkip(el, segments, true, actions, (s) => skipped.push(s)));
    tick(12);
    expect(el.currentTime).toBe(20);
    expect(skipped).toHaveLength(1);
  });

  it("mutes for a mute segment and restores after it", () => {
    const { el, tick } = fakeVideo();
    renderHook(() => useSponsorSkip(el, segments, true, actions));
    tick(35);
    expect(el.muted).toBe(true);
    tick(41);
    expect(el.muted).toBe(false);
  });

  it("leaves a viewer who was already muted muted", () => {
    const { el, tick } = fakeVideo();
    el.muted = true;
    renderHook(() => useSponsorSkip(el, segments, true, actions));
    tick(35);
    expect(el.muted).toBe(true);
    tick(41);
    expect(el.muted).toBe(true);
  });

  it("keeps the viewer's choice when they unmute inside the segment", () => {
    const { el, tick } = fakeVideo();
    renderHook(() => useSponsorSkip(el, segments, true, actions));
    tick(32);
    el.muted = false; // the viewer overrides it
    tick(35);
    expect(el.muted).toBe(false);
  });

  it("unmutes when the player goes away mid-segment", () => {
    const { el, tick } = fakeVideo();
    const { unmount } = renderHook(() => useSponsorSkip(el, segments, true, actions));
    tick(35);
    expect(el.muted).toBe(true);
    unmount();
    expect(el.muted).toBe(false);
  });

  it("does nothing when the preference is off", () => {
    const { el, tick } = fakeVideo();
    renderHook(() => useSponsorSkip(el, segments, false, actions));
    tick(12);
    expect(el.currentTime).toBe(12);
    tick(35);
    expect(el.muted).toBe(false);
  });
});
