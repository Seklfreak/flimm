import { describe, expect, it } from "vitest";
import { waitFor, within } from "@testing-library/react";
import { Route, Routes } from "react-router";
import { Layout } from "./Layout";
import { feed, mockFetch, playlist, renderWithProviders, video } from "@/test/helpers";

function renderLayout(route = "/") {
  const result = renderWithProviders(
    <Routes>
      <Route element={<Layout />}>
        <Route index element={<div>home</div>} />
        <Route path="/playlists/:id" element={<div>playlist page</div>} />
      </Route>
    </Routes>,
    { route },
  );
  // Scope to the desktop sidebar: the mobile tab bar has its own unconditional
  // "Playlists" link, so unscoped queries would match it instead.
  const sidebar = within(result.container.querySelector("aside")!);
  return { ...result, sidebar };
}

describe("Sidebar Playlists group", () => {
  it("renders no Playlists group when nothing is pinned", async () => {
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [],
    });
    const { sidebar } = renderLayout();
    await waitFor(() => expect(sidebar.getByText("Home")).toBeTruthy());
    // The library nav ("Channels / Playlists / History / Search") always has
    // a "Playlists" link; the pinned-playlists group must not add a second
    // "Playlists" text (its section header) when nothing is pinned.
    expect(sidebar.getAllByText("Playlists")).toHaveLength(1);
  });

  it("renders one row per pinned playlist, highlighting the active route", async () => {
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [
        playlist({ id: "p1", name: "Shader Deep Dives", video_count: 14, seen_count: 11 }),
        playlist({ id: "p2", name: "Fully Watched", video_count: 5, seen_count: 5 }),
      ],
    });
    const { sidebar } = renderLayout("/playlists/p1");
    await waitFor(() => expect(sidebar.getByText("Shader Deep Dives")).toBeTruthy());
    // Section header plus the always-present library nav link.
    expect(sidebar.getAllByText("Playlists")).toHaveLength(2);
    expect(sidebar.getByText("Fully Watched")).toBeTruthy();

    const activeLink = sidebar.getByRole("link", { name: /Shader Deep Dives/ });
    expect(activeLink.getAttribute("href")).toBe("/playlists/p1");
    expect(activeLink.className.split(" ")).toContain("bg-raised");

    const inactiveLink = sidebar.getByRole("link", { name: /Fully Watched/ });
    expect(inactiveLink.className.split(" ")).not.toContain("bg-raised");

    // remaining-unseen badge: 14-11=3 shows a number, a fully seen playlist
    // (5-5=0) shows the neutral dot instead of "0".
    expect(activeLink.querySelector(".badge")?.textContent).toBe("3");
    expect(inactiveLink.querySelector(".badge")).toBeNull();
  });
});

describe("Sidebar Continue watching", () => {
  const entry = (id: string, title: string, position: number) => ({
    id,
    video: video({ id: `v-${id}`, title, position, duration: 1000 }),
    played_at: new Date().toISOString(),
    state: "in_progress" as const,
  });
  const page = (items: unknown[]) => ({ items, page: 0, page_size: 30, total: items.length });

  it("renders nothing when no video is in progress", async () => {
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [],
      "GET /api/v1/history": page([]),
    });
    const { sidebar } = renderLayout();
    await waitFor(() => expect(sidebar.getByText("Home")).toBeTruthy());
    expect(sidebar.queryByText("Continue watching")).toBeNull();
  });

  it("lists in-progress videos with their resume position", async () => {
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [],
      "GET /api/v1/history": page([entry("h1", "Half watched", 300)]),
    });
    const { sidebar } = renderLayout();
    await waitFor(() => expect(sidebar.getByText("Continue watching")).toBeTruthy());
    expect(sidebar.getByText("Half watched")).toBeTruthy();
    expect(sidebar.getByText("5:00 / 16:40")).toBeTruthy();
  });

  // The sidebar is a shortcut, not the whole history.
  it("caps the list and links to the full history", async () => {
    const many = Array.from({ length: 9 }, (_, i) => entry(`h${i}`, `Video ${i}`, 60));
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [],
      "GET /api/v1/history": page(many),
    });
    const { sidebar } = renderLayout();
    await waitFor(() => expect(sidebar.getByText("Continue watching")).toBeTruthy());
    expect(sidebar.queryByText("Video 4")).toBeTruthy();
    expect(sidebar.queryByText("Video 5")).toBeNull();
    expect(sidebar.getByText("All")).toBeTruthy();
  });

  it("dismisses an entry through the history endpoint", async () => {
    const { calls } = mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/playlists/pinned": [],
      "GET /api/v1/history": page([entry("h1", "Half watched", 300)]),
      "DELETE /api/v1/history/h1": null,
    });
    const { sidebar } = renderLayout();
    await waitFor(() => expect(sidebar.getByText("Half watched")).toBeTruthy());
    sidebar.getByLabelText("Dismiss Half watched").click();
    await waitFor(() =>
      expect(calls.some((c) => c.url.includes("/history/h1") && (c.init?.method ?? "GET") === "DELETE")).toBe(true),
    );
  });
});
