import { describe, expect, it } from "vitest";
import { screen } from "@testing-library/react";
import { VideoCard, watchHref } from "./VideoCard";
import { renderWithProviders, video } from "@/test/helpers";

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
});
