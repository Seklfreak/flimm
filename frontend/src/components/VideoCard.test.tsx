import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { VideoCard, watchHref } from "./VideoCard";
import { mockFetch, renderWithProviders, video } from "@/test/helpers";

describe("VideoCard", () => {
  it("shows the resume chip, duration, channel link and CC meta", () => {
    renderWithProviders(<VideoCard video={video()} ctx={{ feed: "f1" }} />);
    expect(screen.getByText("Resume · 9:21")).toBeTruthy();
    expect(screen.getByText("24:36")).toBeTruthy();
    const ch = screen.getByRole("link", { name: "Freya Holmér" });
    expect(ch.getAttribute("href")).toBe("/channels/UC1");
    expect(screen.getByText(/CC EN · 3 days ago/)).toBeTruthy();
    for (const l of screen.getAllByRole("link", { name: "The Beauty of Bézier Curves" })) expect(l.getAttribute("href")).toBe("/watch/vid1?feed=f1");
  });
  it("renders seen state without a resume chip", () => {
    renderWithProviders(<VideoCard video={video({ watched: true, position: 0, progress: 1 })} />);
    expect(screen.queryByText(/Resume/)).toBeNull();
    expect(screen.getByText(/seen today/)).toBeTruthy();
  });
  it("watchHref drops empty context keys", () => {
    expect(watchHref({ id: "x" }, { feed: undefined, playlist: "p" })).toBe("/watch/x?playlist=p");
  });
  it("watchHref round-trips the audio param", () => {
    expect(watchHref({ id: "x" }, { audio: "1" })).toBe("/watch/x?audio=1");
    expect(watchHref({ id: "x" }, { playlist: "p", audio: "1" })).toBe("/watch/x?playlist=p&audio=1");
    expect(watchHref({ id: "x" }, { audio: undefined })).toBe("/watch/x");
  });
  it('dismisses a video: clicking "Not interested" calls the dismiss endpoint', async () => {
    const { calls } = mockFetch({ "POST /api/v1/videos/vid1/dismiss": { dismissed: true } });
    renderWithProviders(<VideoCard video={video()} />);
    fireEvent.click(screen.getByRole("button", { name: "Not interested" }));
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/videos/vid1/dismiss") && c.init?.method === "POST")).toBe(true),
    );
  });
  it('shows a dismissed video as "Hidden from feeds" and restores it', async () => {
    const { calls } = mockFetch({ "DELETE /api/v1/videos/vid1/dismiss": { dismissed: false } });
    renderWithProviders(<VideoCard video={video({ dismissed: true })} />);
    expect(screen.getByText(/Hidden from feeds/)).toBeTruthy();
    // Nothing left to dismiss on an already-dismissed card.
    expect(screen.queryByRole("button", { name: "Not interested" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/videos/vid1/dismiss") && c.init?.method === "DELETE")).toBe(true),
    );
  });
});
