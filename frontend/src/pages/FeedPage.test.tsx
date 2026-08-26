import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router";
import FeedPage from "./FeedPage";
import { feed, mockFetch, renderWithProviders, video } from "@/test/helpers";

describe("FeedPage", () => {
  it("loads the feed header and the first page of videos", async () => {
    const { calls } = mockFetch({
      "GET /api/v1/feeds": [feed(), feed({ id: "everything", name: "Everything", pinned: false, unseen_count: 19 })],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": { items: [video(), video({ id: "vid2", title: "Why Your Kubernetes Cluster Is Slow", position: 0, progress: 0 })], page: 0, page_size: 30, total: 2 },
    });
    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    expect(screen.getAllByText("Home").length).toBeGreaterThan(0);
    expect(screen.getAllByText(/7 unseen · 6 channels/).length).toBeGreaterThan(0);
    expect(screen.getByText("Why Your Kubernetes Cluster Is Slow")).toBeTruthy();
    // Default view is the feed's own default (no ?view=) until the user picks a tab.
    const videosCall = calls.find((c) => c.url.includes("/feeds/f1/videos"));
    expect(videosCall?.url).not.toContain("view=");
    expect(videosCall?.url).toContain("page=0");
  });

  it("shows an empty state when caught up", async () => {
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": { items: [], page: 0, page_size: 30, total: 0 },
    });
    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );
    await waitFor(() => expect(screen.getByText("All caught up")).toBeTruthy());
  });
});
