import { describe, expect, it } from "vitest";
import { CUE_LINE, cueFontSize, cueLineOverChrome, MIN_CUE_PX } from "./cueSize";

describe("cueFontSize", () => {
  it("scales with the player box, so fullscreen is bigger than inline", () => {
    const inline = cueFontSize(506, "medium");
    const fullscreen = cueFontSize(1080, "medium");
    expect(inline).toBe(18);
    expect(fullscreen).toBe(38);
    // The regression this replaces: every size rendered ~11px whatever the box.
    expect(fullscreen).toBeGreaterThan(inline * 2);
  });

  it("keeps the three settings apart at every size", () => {
    for (const height of [400, 720, 1440]) {
      const small = cueFontSize(height, "small");
      const medium = cueFontSize(height, "medium");
      const large = cueFontSize(height, "large");
      expect(small).toBeLessThan(medium);
      expect(medium).toBeLessThan(large);
    }
  });

  it("stops shrinking at the readable floor, one per setting", () => {
    expect(cueFontSize(200, "small")).toBe(MIN_CUE_PX.small);
    // A shared floor would make small and medium identical in a small window.
    expect(cueFontSize(200, "medium")).toBe(MIN_CUE_PX.medium);
    // A box that has not been laid out yet still gets a usable size.
    expect(cueFontSize(0, "large")).toBe(MIN_CUE_PX.large);
  });
});

describe("cueLineOverChrome", () => {
  // The bug: a 306px player's control bar is 73px, and CUE_LINE lifts cues
  // about two 14px line boxes — 36px — so a paused viewer read their captions
  // through the scrubber.
  it("lifts cues clear of the control bar", () => {
    const line = cueLineOverChrome(73, 14);
    expect(line).toBeLessThanOrEqual(-6);
    // Far enough up: (|line| - 1) line boxes must clear the bar.
    expect((Math.abs(line) - 1) * 14 * 1.3).toBeGreaterThan(73);
  });

  // A taller bar (fullscreen) pushes further, with no constant to update.
  it("scales with the bar and with the cue size", () => {
    expect(cueLineOverChrome(120, 14)).toBeLessThan(cueLineOverChrome(73, 14));
    // Bigger cues cover the same bar in fewer lines.
    expect(cueLineOverChrome(73, 24)).toBeGreaterThan(cueLineOverChrome(73, 14));
  });

  // Before anything has been measured, the idle position stands.
  it("falls back to the idle line", () => {
    expect(cueLineOverChrome(0, 14)).toBe(CUE_LINE);
    expect(cueLineOverChrome(73, 0)).toBe(CUE_LINE);
  });

  // Never lower than the idle position, whatever the arithmetic says.
  it("never drops a cue below where it sits with no chrome", () => {
    expect(cueLineOverChrome(4, 40)).toBeLessThanOrEqual(CUE_LINE);
  });
});
