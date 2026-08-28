import { beforeEach, describe, expect, it } from "vitest";
import { fireEvent, screen, waitFor } from "@testing-library/react";
import { UpNextPanel, UP_NEXT_STORAGE_KEY, slots } from "./UpNextPanel";
import { mockFetch, renderWithProviders, video } from "@/test/helpers";

const items = [
  video({ id: "a", title: "First up" }),
  video({ id: "b", title: "Second up" }),
  video({ id: "c", title: "Third up" }),
];

function renderPanel() {
  return renderWithProviders(
    <UpNextPanel
      items={items}
      title="Up next"
      isLoading={false}
      hasNextPage={false}
      isFetchingNextPage={false}
      fetchNextPage={() => {}}
      autoplay={false}
      onAutoplay={() => {}}
    />,
  );
}

beforeEach(() => window.localStorage.clear());

describe("dismissing from up next", () => {
  it("takes the video out of the list and leaves an undo in its place", async () => {
    const { calls } = mockFetch({ "POST /api/v1/videos/b/dismiss": undefined });
    renderPanel();

    fireEvent.click(screen.getAllByLabelText("Not interested")[1]);

    await waitFor(() => expect(calls.some((c) => c.url.endsWith("/videos/b/dismiss"))).toBe(true));
    expect(screen.queryByText("Second up")).toBeNull();
    expect(screen.getByText("First up")).toBeTruthy();
    expect(screen.getByText("Third up")).toBeTruthy();
    expect(screen.getByText("Hidden from feeds")).toBeTruthy();
  });

  it("puts it back on undo", async () => {
    const { calls } = mockFetch({
      "POST /api/v1/videos/b/dismiss": undefined,
      "DELETE /api/v1/videos/b/dismiss": undefined,
    });
    renderPanel();

    fireEvent.click(screen.getAllByLabelText("Not interested")[1]);
    await waitFor(() => expect(screen.getByText("Undo")).toBeTruthy());
    fireEvent.click(screen.getByText("Undo"));

    await waitFor(() => expect(calls.some((c) => c.init?.method === "DELETE")).toBe(true));
    expect(screen.getByText("Second up")).toBeTruthy();
    expect(screen.queryByText("Hidden from feeds")).toBeNull();
  });

  it("restores the row when the server refuses the dismissal", async () => {
    mockFetch({}); // every call 404s
    renderPanel();

    fireEvent.click(screen.getAllByLabelText("Not interested")[0]);

    await waitFor(() => expect(screen.getByText("First up")).toBeTruthy());
    expect(screen.queryByText("Hidden from feeds")).toBeNull();
  });
});

describe("collapsing the sidebar", () => {
  it("hides the list, and remembers it for the next video", () => {
    mockFetch({});
    const { unmount } = renderPanel();

    fireEvent.click(screen.getByLabelText("Hide up next"));
    expect(screen.queryByText("First up")).toBeNull();
    expect(window.localStorage.getItem(UP_NEXT_STORAGE_KEY)).toBe("1");

    unmount();
    renderPanel();
    expect(screen.queryByText("First up")).toBeNull();
    expect(screen.getByLabelText("Show up next")).toBeTruthy();
  });

  it("brings the list back", () => {
    mockFetch({});
    renderPanel();

    fireEvent.click(screen.getByLabelText("Hide up next"));
    fireEvent.click(screen.getByLabelText("Show up next"));

    expect(screen.getByText("First up")).toBeTruthy();
    expect(window.localStorage.getItem(UP_NEXT_STORAGE_KEY)).toBe("0");
  });
});

describe("slots", () => {
  it("keeps an undo where its video was, not at the end", () => {
    const visible = [items[0], items[2]];
    const laid = slots(visible, [{ video: items[1], index: 1 }]);
    expect(laid.map((s) => ("removed" in s ? `undo:${s.removed.video.id}` : s.video.id))).toEqual(["a", "undo:b", "c"]);
  });

  it("puts an undo past the end of a shrunken list at the end", () => {
    const laid = slots([], [{ video: items[0], index: 4 }]);
    expect(laid).toHaveLength(1);
  });
});
