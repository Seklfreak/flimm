import { describe, expect, it } from "vitest";
import { screen, waitFor, within } from "@testing-library/react";
import AdminPage from "./AdminPage";
import { mockFetch, renderWithProviders } from "@/test/helpers";
import type { LiveResponse } from "@/lib/api";

const prefs = { theme: "system" } as never;
const admin = { id: "u1", name: "Dev User", email: "dev@localhost", is_admin: true, prefs };
const viewer = { ...admin, is_admin: false };

const live: LiveResponse = {
  now: "2026-09-03T20:00:30Z",
  sessions: [
    {
      user_id: "u1",
      user: "viewer@example.com",
      video_id: "v1",
      title: "Braising, the long way",
      channel_name: "Slow Kitchen",
      client: "tvos",
      device: "Living Room",
      position: 300,
      duration: 600,
      paused: false,
      started_at: "2026-09-03T19:55:00Z",
      updated_at: "2026-09-03T20:00:20Z",
      streaming: true,
      bytes: 3_145_728,
      stalls: 2,
      last_stall: "encoder_behind",
      delivery: {
        kind: "rendition",
        height: 720,
        job: { video_id: "v1", height: 720, segments: 150, progress: 0.31, encoder_segment: 107 },
      },
    },
  ],
  jobs: [
    { video_id: "v1", height: 720, segments: 150, progress: 0.31, encoder_segment: 107 },
    { video_id: "v2", height: 480, segments: 90, progress: 0, encoder_segment: -1 },
  ],
  stalls: [
    {
      at: "2026-09-03T20:00:10Z",
      video_id: "v1",
      position: 118,
      seconds: 2.4,
      height: 720,
      client: "tvos",
      reason: "encoder_behind",
      segment: 29,
      encoder: 25,
    },
  ],
};

describe("AdminPage", () => {
  // The three questions the page exists to answer, in one read: who is
  // watching, what it is costing the server, and what went wrong.
  it("says who is watching, how it is being delivered, and what stalled", async () => {
    mockFetch({ "GET /api/v1/me": admin, "GET /api/v1/admin/sessions": live });
    renderWithProviders(<AdminPage />);

    await waitFor(() => expect(screen.getByText("viewer@example.com")).toBeTruthy());
    expect(screen.getByText("Braising, the long way")).toBeTruthy();
    // The device's own name beats the platform, which is what an admin looking
    // for a particular television in the house actually needs.
    expect(screen.getByText("Living Room · Apple TV")).toBeTruthy();
    expect(screen.getByText(/Transcoded 720p/)).toBeTruthy();
    expect(screen.getByText(/3.0 MB sent/)).toBeTruthy();
    expect(screen.getByText(/encoding segment 107 of 150 · 31% derived/)).toBeTruthy();
    expect(screen.getByText(/2 stalls · last one the encoder was behind the viewer/)).toBeTruthy();
    // And the stall itself in the table below, said the same way.
    expect(within(screen.getByRole("table")).getByText("the encoder was behind the viewer")).toBeTruthy();
  });

  // A transcode nobody is attached to is the thing that is otherwise invisible:
  // the machine encoding for a viewer who left.
  it("separates a transcode no session is waiting on", async () => {
    mockFetch({ "GET /api/v1/me": admin, "GET /api/v1/admin/sessions": live });
    renderWithProviders(<AdminPage />);

    await waitFor(() => expect(screen.getByText("Transcoding for nobody")).toBeTruthy());
    // The rung a session is already waiting on is not repeated here; the one
    // nobody is attached to is the whole point of the section.
    const orphans = screen.getByText("Transcoding for nobody").closest("section") as HTMLElement;
    expect(within(orphans).getByText("v2")).toBeTruthy();
    expect(within(orphans).getByText(/480p · queued for the transcode slot/)).toBeTruthy();
    expect(within(orphans).queryByText("v1")).toBeNull();
  });

  // This is every account's playback, so it is not for every account.
  it("tells a non-admin it is not theirs to read", async () => {
    mockFetch({ "GET /api/v1/me": viewer });
    renderWithProviders(<AdminPage />);

    await waitFor(() => expect(screen.getByText("Administrators only")).toBeTruthy());
    expect(screen.queryByText("Playing now")).toBeNull();
  });
});
