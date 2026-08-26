import { describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router";
import PlaylistPage from "./PlaylistPage";
import { mockFetch, playlist, renderWithProviders, video } from "@/test/helpers";

function renderPlaylist(music: boolean) {
  const p = { ...playlist({ music }), items: [{ position: 1, video: video() }] };
  mockFetch({
    "GET /api/v1/playlists/p1": p,
  });
  return renderWithProviders(
    <Routes>
      <Route path="/playlists/:id" element={<PlaylistPage />} />
    </Routes>,
    { route: "/playlists/p1" },
  );
}

describe("PlaylistPage — music playlists", () => {
  it("shows the unseen filter and seen counts for a regular playlist", async () => {
    renderPlaylist(false);
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    expect(screen.getByText("Unseen only")).toBeTruthy();
    expect(screen.getByText(/11 seen, 1 in progress/)).toBeTruthy();
  });

  it("hides the unseen filter and seen counts for a music playlist", async () => {
    renderPlaylist(true);
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    expect(screen.queryByText("Unseen only")).toBeNull();
    expect(screen.queryByText(/seen/)).toBeNull();
  });

  it("renders Play, not Seen/Resume, for a row in a music playlist", async () => {
    renderPlaylist(true);
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    // The header offers Play too, so scope to the row's own action button.
    const playLinks = screen.getAllByRole("link", { name: /Play/ });
    expect(playLinks.length).toBeGreaterThan(0);
    expect(screen.queryByRole("button", { name: /Seen/ })).toBeNull();
    expect(screen.queryByRole("link", { name: /Resume/ })).toBeNull();
  });

  it("labels the toggle Music and shows both effects in its title", async () => {
    renderPlaylist(false);
    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    const toggle = screen.getByText("Music").closest("button")!;
    expect(toggle.getAttribute("title")).toMatch(/audio only/);
    expect(toggle.getAttribute("title")).toMatch(/no watch history/);
  });
});
