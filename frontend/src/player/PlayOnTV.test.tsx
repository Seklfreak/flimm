import { describe, expect, it, vi } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { PlayOnTV } from "./PlayOnTV";
import { mockFetch, renderWithProviders } from "@/test/helpers";

const receiver = (id: string, device: string) => ({ id, device, platform: "tvos", updated_at: new Date().toISOString() });

describe("play on TV", () => {
  it("draws nothing when no television is available", async () => {
    const { calls } = mockFetch({ "GET /api/v1/playback/sessions": { sessions: [], receivers: [], version: 1 } });
    const { container } = renderWithProviders(<PlayOnTV videoId="v1" ctx={{}} currentTime={() => 0} onCast={() => {}} />);
    await waitFor(() => expect(calls.length).toBe(1));
    expect(container.textContent).toBe("");
  });

  it("sends the video, the position and the context, then pauses here", async () => {
    const { calls } = mockFetch({
      "GET /api/v1/playback/sessions": { sessions: [], receivers: [receiver("r1", "Living Room")], version: 2 },
      "POST /api/v1/playback/receivers/r1/commands": { seq: 1 },
    });
    const onCast = vi.fn();
    renderWithProviders(
      <PlayOnTV videoId="v1" ctx={{ playlist: "pl1", shuffle: "abc", audio: "1" }} currentTime={() => 61.5} onCast={onCast} />,
    );

    // One television: the button names it and sends straight away.
    fireEvent.click(await screen.findByText("Play on Living Room"));

    await screen.findByText("Playing on Living Room");
    expect(onCast).toHaveBeenCalledOnce();
    const post = calls.find((c) => c.init?.method === "POST");
    expect(JSON.parse(String(post?.init?.body))).toEqual({
      kind: "open",
      video_id: "v1",
      position: 61.5,
      context: { playlist: "pl1", shuffle: "abc", audio: true },
    });
  });

  it("offers a choice when there is more than one, and keeps playing when the cast fails", async () => {
    mockFetch({
      "GET /api/v1/playback/sessions": { sessions: [], receivers: [receiver("r1", "Bedroom"), receiver("r2", "Living Room")], version: 3 },
      "POST /api/v1/playback/receivers/r2/commands": () => {
        throw new Error("down");
      },
    });
    const onCast = vi.fn();
    renderWithProviders(<PlayOnTV videoId="v1" ctx={{}} currentTime={() => 0} onCast={onCast} />);

    fireEvent.click(await screen.findByText("Play on TV"));
    fireEvent.click(await screen.findByRole("menuitem", { name: "Living Room" }));

    await screen.findByText("Couldn't reach Living Room");
    expect(onCast).not.toHaveBeenCalled();
  });
});
