import { afterEach, describe, expect, it } from "vitest";
import { resetAnalyticsForTests, routePattern, trackEvent, trackScreen } from "./analytics";

afterEach(() => resetAnalyticsForTests());

/** Stands in for the tracker script, capturing what each call would post. */
function fakeUmami() {
  const sent: Record<string, unknown>[] = [];
  window.umami = { track: (build) => sent.push(build({ website: "w", hostname: "flimm.example.com" })) };
  return sent;
}

describe("routePattern", () => {
  it("reports ids as the pattern, never the value", () => {
    expect(routePattern("/watch/dQw4w9WgXcQ")).toEqual({ url: "/watch/:id", title: "Watch" });
    expect(routePattern("/channels/UC123")).toEqual({ url: "/channels/:id", title: "Channel" });
    expect(routePattern("/playlists/PL123")).toEqual({ url: "/playlists/:id", title: "Playlist" });
  });

  it("keeps the feed editor apart from the feed", () => {
    expect(routePattern("/feeds/abc")).toEqual({ url: "/feeds/:id", title: "Feed" });
    expect(routePattern("/feeds/abc/edit")).toEqual({ url: "/feeds/:id/edit", title: "Edit feed" });
    expect(routePattern("/feeds/new")).toEqual({ url: "/feeds/new", title: "New feed" });
  });

  it("matches the flat routes, with or without a trailing slash", () => {
    expect(routePattern("/")).toEqual({ url: "/", title: "Home" });
    expect(routePattern("/history/")).toEqual({ url: "/history", title: "History" });
    expect(routePattern("/settings")).toEqual({ url: "/settings", title: "Settings" });
  });

  it("skips anything it does not recognise", () => {
    expect(routePattern("/nope")).toBeNull();
    expect(routePattern("/watch/a/b")).toBeNull();
  });
});

describe("tracking", () => {
  it("posts the pattern as the url, and reuses it for later events", () => {
    const sent = fakeUmami();
    trackScreen("/watch/:id", "Watch");
    trackEvent("play", { kind: "video", audio: "no" });

    expect(sent[0]).toMatchObject({ url: "/watch/:id", title: "Watch" });
    expect(sent[0].name).toBeUndefined();
    expect(sent[1]).toMatchObject({ url: "/watch/:id", name: "play", data: { kind: "video", audio: "no" } });
  });

  it("holds calls made before the tracker loads and sends them in order", () => {
    trackScreen("/", "Home");
    trackEvent("search", { scope: "all" });
    const sent = fakeUmami();
    expect(sent).toHaveLength(0);

    trackScreen("/search", "Search");
    expect(sent.map((p) => [p.url, p.name])).toEqual([
      ["/", undefined],
      ["/", "search"],
      ["/search", undefined],
    ]);
  });

  it("drops an event with no data rather than sending an empty object", () => {
    const sent = fakeUmami();
    trackEvent("feed-created");
    expect(sent[0]).toMatchObject({ name: "feed-created" });
    expect(sent[0].data).toBeUndefined();
  });

  it("caps the backlog rather than growing while no tracker is there", () => {
    for (let i = 0; i < 50; i++) trackEvent("play", { kind: "video", audio: "no" });
    const sent = fakeUmami();
    trackScreen("/", "Home");
    // The 20 most recent queued calls, then the live one.
    expect(sent).toHaveLength(21);
  });
});
