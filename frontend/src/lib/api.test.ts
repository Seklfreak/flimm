import { describe, expect, it } from "vitest";
import { api } from "./api";
import { mockFetch } from "@/test/helpers";

// The heartbeat only works for music playlists if the playlist context makes
// it onto the request (docs/api.md: `?playlist=<id>` on the progress
// heartbeat) — the server has no other way to tell playback apart.
describe("api.progress", () => {
  it("appends ?playlist= when given a playlist id", async () => {
    const { calls } = mockFetch({
      "POST /api/v1/videos/vid1/progress": { position: 42, watched: false },
    });
    await api.progress("vid1", 42, "p1");
    expect(calls[0].url).toBe("/api/v1/videos/vid1/progress?playlist=p1");
  });

  it("omits the playlist param when not given", async () => {
    const { calls } = mockFetch({
      "POST /api/v1/videos/vid1/progress": { position: 42, watched: false },
    });
    await api.progress("vid1", 42);
    expect(calls[0].url).toBe("/api/v1/videos/vid1/progress");
  });
});
