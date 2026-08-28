import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router";
import ChannelPage from "./ChannelPage";
import { channel, mockFetch, renderWithProviders, video } from "@/test/helpers";

describe("ChannelPage", () => {
  it("offers to restore a dismissed video instead of hiding it", async () => {
    // Unlike a feed, a channel page keeps a dismissed video in its list
    // (docs/api.md "dismissed") — it just says so and offers a way back.
    const { calls } = mockFetch({
      "GET /api/v1/feeds": [],
      "GET /api/v1/channels/UC1": channel(),
      "GET /api/v1/channels/UC1/videos": { items: [video({ dismissed: true })], page: 0, page_size: 30, total: 1 },
      "DELETE /api/v1/videos/vid1/dismiss": { dismissed: false },
    });
    renderWithProviders(
      <Routes>
        <Route path="/channels/:id" element={<ChannelPage />} />
      </Routes>,
      { route: "/channels/UC1" },
    );
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    expect(screen.getByText(/Hidden from feeds/)).toBeTruthy();

    fireEvent.click(screen.getByRole("button", { name: "Restore" }));
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/videos/vid1/dismiss") && c.init?.method === "DELETE")).toBe(true),
    );
    // The card never left the page — nothing to undo, it's back to normal.
    expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy();
  });
});
