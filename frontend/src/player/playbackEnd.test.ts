import { describe, expect, it } from "vitest";
import { playbackEnd } from "./playbackEnd";

describe("playbackEnd", () => {
  it("advances only when autoplay has somewhere to go", () => {
    expect(playbackEnd(true, true)).toBe("advance");
  });

  it("finishes when autoplay is off, however much is queued", () => {
    expect(playbackEnd(false, true)).toBe("finished");
  });

  it("finishes at the end of the list even with autoplay on", () => {
    expect(playbackEnd(true, false)).toBe("finished");
  });

  it("finishes a video watched with no context at all", () => {
    expect(playbackEnd(false, false)).toBe("finished");
  });
});
