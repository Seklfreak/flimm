import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
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

  it("dismissing a card pulls it from the feed with an Undo, which puts it back", async () => {
    // The server drops a dismissed video from the feed, and dismissing
    // invalidates the query — so the refetch must come back *without* it here
    // too. A static list would hide the bug this test exists for: an Undo
    // rendered from the fetched items disappears the moment the refetch lands.
    let dismissed = false;
    const { calls } = mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": () => ({
        items: dismissed
          ? [video({ id: "vid2", title: "Why Your Kubernetes Cluster Is Slow", position: 0, progress: 0 })]
          : [video(), video({ id: "vid2", title: "Why Your Kubernetes Cluster Is Slow", position: 0, progress: 0 })],
        page: 0,
        page_size: 30,
        total: dismissed ? 1 : 2,
      }),
      "POST /api/v1/videos/vid1/dismiss": () => {
        dismissed = true;
        return { dismissed: true };
      },
      "DELETE /api/v1/videos/vid1/dismiss": () => {
        dismissed = false;
        return { dismissed: false };
      },
    });
    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());

    // A feed never returns a dismissed video, so the card must come out of
    // this list immediately — not after a round trip to the server.
    fireEvent.click(screen.getAllByRole("button", { name: "Not interested" })[0]);
    expect(screen.queryByRole("link", { name: "The Beauty of Bézier Curves" })).toBeNull();
    expect(screen.getByText("Why Your Kubernetes Cluster Is Slow")).toBeTruthy();
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/videos/vid1/dismiss") && c.init?.method === "POST")).toBe(true),
    );
    // The refetch has now returned the shorter list; the Undo has to survive
    // it, because it is the only way back.
    await waitFor(() => expect(screen.getByRole("button", { name: "Undo" })).toBeTruthy());

    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    expect(screen.getAllByRole("link", { name: "The Beauty of Bézier Curves" }).length).toBeGreaterThan(0);
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/videos/vid1/dismiss") && c.init?.method === "DELETE")).toBe(true),
    );
  });
});
