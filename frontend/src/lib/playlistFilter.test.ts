import { describe, expect, it } from "vitest";
import { video } from "@/test/helpers";
import { filterPlaylistItems } from "./playlistFilter";

const item = (position: number, title: string, channel: string) => ({
  position,
  video: video({ id: `v${position}`, title, channel: { id: `UC${position}`, name: channel, thumb_url: "" } }),
});

const items = [
  item(0, "Winter", "Daughter"),
  item(1, "Halo", "Beyoncé"),
  item(2, "Winter Song", "Sara Bareilles"),
];

const ids = (q: string) => filterPlaylistItems(items, q).map((i) => i.position);

describe("filterPlaylistItems", () => {
  it("returns everything for an empty or blank query", () => {
    expect(ids("")).toEqual([0, 1, 2]);
    expect(ids("   ")).toEqual([0, 1, 2]);
  });
  it("matches title or channel, ignoring case", () => {
    expect(ids("WINTER")).toEqual([0, 2]);
    expect(ids("daughter")).toEqual([0]);
  });
  it("needs every word, in any order, across title and channel", () => {
    expect(ids("winter daughter")).toEqual([0]);
    expect(ids("daughter   winter")).toEqual([0]);
    expect(ids("winter halo")).toEqual([]);
  });
  it("ignores accents on either side", () => {
    expect(ids("beyonce")).toEqual([1]);
    expect(ids("béyoncé")).toEqual([1]);
  });
  it("keeps playlist order and positions", () => {
    expect(filterPlaylistItems(items, "song")[0].position).toBe(2);
  });
});
