import { describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import SettingsPage from "./SettingsPage";
import { mockFetch, renderWithProviders } from "@/test/helpers";
import type { Prefs } from "@/lib/api";

const prefs: Prefs = {
  autoplay: true,
  playback_speed: 1,
  subtitle_lang: "en",
  subtitle_size: "medium",
  skip_sponsors: true,
  everything_sort: "newest",
  everything_hide_seen: true,
  everything_include_shorts: false,
  theme: "system",
};

const me = { id: "u1", name: "Dev User", email: "dev@localhost", is_admin: true, prefs };

describe("SettingsPage", () => {
  // Every preference the API carries has to be reachable somewhere, or it is a
  // setting only the other clients can change.
  it("offers every account preference", async () => {
    mockFetch({ "GET /api/v1/me": me });
    renderWithProviders(<SettingsPage />);

    await waitFor(() => expect(screen.getByText("Autoplay next video")).toBeTruthy());
    for (const label of [
      "Playback speed",
      "Skip sponsor segments",
      "Language",
      "Size",
      "Sort",
      "Hide seen",
      "Include Shorts",
      "Theme",
    ]) {
      expect(screen.getByText(label)).toBeTruthy();
    }
  });

  it("writes a toggle through to PATCH /me/prefs", async () => {
    const { calls } = mockFetch({
      "GET /api/v1/me": me,
      "PATCH /api/v1/me/prefs": { ...prefs, skip_sponsors: false },
    });
    renderWithProviders(<SettingsPage />);

    await waitFor(() => expect(screen.getByLabelText("Skip sponsor segments")).toBeTruthy());
    fireEvent.click(screen.getByLabelText("Skip sponsor segments"));

    await waitFor(() => {
      const patch = calls.find((c) => (c.init?.method ?? "GET").toUpperCase() === "PATCH");
      expect(patch).toBeTruthy();
      expect(JSON.parse(String(patch?.init?.body))).toEqual({ skip_sponsors: false });
    });
  });

  it("writes the theme through, which is otherwise unreachable on the web", async () => {
    const { calls } = mockFetch({
      "GET /api/v1/me": me,
      "PATCH /api/v1/me/prefs": { ...prefs, theme: "dark" },
    });
    renderWithProviders(<SettingsPage />);

    await waitFor(() => expect(screen.getByText("Dark")).toBeTruthy());
    fireEvent.click(screen.getByText("Dark"));

    await waitFor(() => {
      const patch = calls.find((c) => (c.init?.method ?? "GET").toUpperCase() === "PATCH");
      expect(JSON.parse(String(patch?.init?.body))).toEqual({ theme: "dark" });
    });
  });
});
