import { describe, expect, it } from "vitest";
import { volumeFor } from "./loudness";

describe("volumeFor", () => {
  it("turns decibels into the scale volume takes", () => {
    expect(volumeFor(-6)).toBeCloseTo(0.501, 3);
    expect(volumeFor(-20)).toBeCloseTo(0.1, 3);
    expect(volumeFor(0)).toBe(1);
  });

  it("plays a video untouched when nothing has been measured", () => {
    expect(volumeFor(undefined)).toBe(1);
    expect(volumeFor(NaN)).toBe(1);
  });

  // The server never sends one, and an element cannot do it anyway: volume
  // stops at 1, so a positive gain would silently be a no-op rather than the
  // boost it claims to be.
  it("never amplifies", () => {
    expect(volumeFor(6)).toBe(1);
  });
});
