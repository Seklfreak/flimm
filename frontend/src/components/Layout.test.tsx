import { describe, expect, it } from "vitest";
import { waitFor, within } from "@testing-library/react";
import { Route, Routes } from "react-router";
import { Layout } from "./Layout";
import { feed, mockFetch, playlist, renderWithProviders } from "@/test/helpers";

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
