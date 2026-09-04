import { describe, expect, it } from "vitest";
import { render } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { useProgressHeartbeat } from "./useProgressHeartbeat";
import { keys } from "@/lib/queries";
import { mockFetch, video } from "@/test/helpers";
import type { Video } from "@/lib/api";

function Harness({ el, id }: { el: HTMLVideoElement; id: string }) {
  useProgressHeartbeat(el, id);
  return null;
}

// jsdom's media element has no clock; give it one.
function playing(at: number): HTMLVideoElement {
  const el = document.createElement("video");
  Object.defineProperty(el, "currentTime", { value: at, writable: true });
  return el;
}

describe("leaving the watch page", () => {
  it("carries the position into the cached detail, so coming back resumes from it", () => {
    mockFetch({ "POST /api/v1/videos/v1/progress": { watched: false } });
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    // What the page was opened with: the start.
    const detail = { ...video({ id: "v1", duration: 100, position: 0, progress: 0 }), subtitles: [], playlists: [] } as unknown as Video;
    qc.setQueryData(keys.video("v1"), detail);

    const { unmount } = render(
      <QueryClientProvider client={qc}>
        <Harness el={playing(42)} id="v1" />
      </QueryClientProvider>,
    );
    unmount();

    const after = qc.getQueryData<Video>(keys.video("v1"));
    expect(after?.position).toBe(42);
    expect(after?.progress).toBeCloseTo(0.42);
  });
});
