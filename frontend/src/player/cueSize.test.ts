import { describe, expect, it } from "vitest";
import { cueFontSize, MIN_CUE_PX } from "./cueSize";

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
