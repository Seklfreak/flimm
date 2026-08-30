import { describe, expect, it } from "vitest";
import { commentWhen, initial } from "./Comments";
import type { Comment } from "@/lib/api";

const base: Comment = {
  id: "c1",
  author: "@someone",
  author_id: "UC-someone",
  text: "Worth the wait.",
  likes: 3,
  published: null,
  time_text: "",
  hearted: false,
  from_uploader: false,
  replies: [],
};

describe("commentWhen", () => {
  const now = new Date("2026-08-29T12:00:00Z");

  it("uses the archived date when there is one", () => {
    expect(commentWhen({ ...base, published: "2026-08-28T12:00:00Z" }, now)).toBe("yesterday");
  });

  // Older downloads kept only "2 days ago", and that beats showing nothing.
  it("falls back to upstream's own wording", () => {
    expect(commentWhen({ ...base, time_text: "2 days ago" }, now)).toBe("2 days ago");
  });

  it("says nothing when the archive kept neither", () => {
    expect(commentWhen(base, now)).toBe("");
  });
});

describe("initial", () => {
  it("skips the @ that every author name starts with", () => {
    expect(initial("@someone")).toBe("S");
    expect(initial("Someone Else")).toBe("S");
  });

  it("has something to draw for an author with no name", () => {
    expect(initial("")).toBe("?");
    expect(initial("@")).toBe("?");
  });
});
