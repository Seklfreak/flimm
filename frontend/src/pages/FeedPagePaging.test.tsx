import { afterEach, describe, expect, it } from "vitest";
import { screen, waitFor } from "@testing-library/react";
import { Route, Routes } from "react-router";
import FeedPage from "./FeedPage";
import { feed, mockFetch, renderWithProviders, video } from "@/test/helpers";

/// A stand-in for the browser's IntersectionObserver that behaves like the
/// real one in the way that matters here: the first callback arrives *after*
/// `observe()` returns, not during it. An observer that is torn down and
/// rebuilt on every render can be starved by exactly that gap, which is what
/// "scrolling never loads more" looks like from the sofa.
class AsyncIntersectionObserver {
  static live = 0;
  static created = 0;
  private timer: ReturnType<typeof setTimeout> | undefined;

  constructor(private cb: (entries: { isIntersecting: boolean }[]) => void) {}

  observe() {
    AsyncIntersectionObserver.live += 1;
    AsyncIntersectionObserver.created += 1;
    this.timer = setTimeout(() => this.cb([{ isIntersecting: true }]), 0);
  }

  disconnect() {
    AsyncIntersectionObserver.live -= 1;
    clearTimeout(this.timer);
  }

  unobserve() {}
  takeRecords() {
    return [];
  }
}

// The setup file installs a *no-op* IntersectionObserver so components can
// mount, which means nothing has ever exercised this path. Swap in one that
// actually reports an intersection — by assignment, since that property is
// writable but not configurable.
type ObserverGlobal = { IntersectionObserver: unknown };
const realIO = (window as unknown as ObserverGlobal).IntersectionObserver;
const useFakeObserver = () => {
  (window as unknown as ObserverGlobal).IntersectionObserver = AsyncIntersectionObserver;
};

afterEach(() => {
  (window as unknown as ObserverGlobal).IntersectionObserver = realIO;
  AsyncIntersectionObserver.live = 0;
  AsyncIntersectionObserver.created = 0;
});

describe("feed pagination", () => {
  it("keeps loading pages while the sentinel is in view", async () => {
    useFakeObserver();
    // Twelve videos in pages of four: the sentinel has to fire three times.
    const all = Array.from({ length: 12 }, (_, i) =>
      video({ id: `vid${i}`, title: `Video number ${i}`, position: 0, progress: 0 }),
    );
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": (url: string) => {
        const page = Number(new URL(url, "http://x").searchParams.get("page") ?? 0);
        return { items: all.slice(page * 4, page * 4 + 4), page, page_size: 4, total: all.length };
      },
    });

    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );

    await waitFor(() => expect(screen.getByText("Video number 0")).toBeTruthy());
    // The last page is the proof, and it is why the observer is rebuilt on
    // every render: it only reports a *change* in intersection, so a sentinel
    // that stayed in view fires once and never again unless re-armed.
    await waitFor(() => expect(screen.getByText("Video number 11")).toBeTruthy(), { timeout: 3000 });
  });

  // The server composes lazily, so `total` is a floor while more remains:
  // a client that paged on the arithmetic would stop after the first page.
  it("keeps paging while the server says has_more, whatever total says", async () => {
    useFakeObserver();
    const all = Array.from({ length: 9 }, (_, i) => video({ id: `vid${i}`, title: `Video number ${i}`, position: 0, progress: 0 }));
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": (url: string) => {
        const page = Number(new URL(url, "http://x").searchParams.get("page") ?? 0);
        const items = all.slice(page * 3, page * 3 + 3);
        const has_more = (page + 1) * 3 < all.length;
        // total is only ever "what has been walked so far", never the length.
        return { items, page, page_size: 3, total: page * 3 + items.length, has_more };
      },
    });
    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );
    await waitFor(() => expect(screen.getByText("Video number 8")).toBeTruthy());
  });

  // Following the server's cursor is what keeps a deep page as cheap as the
  // first; asking for page=40 instead makes the server walk forty pages.
  it("follows next_cursor instead of asking for an offset", async () => {
    useFakeObserver();
    const all = Array.from({ length: 9 }, (_, i) => video({ id: `vid${i}`, title: `Video number ${i}`, position: 0, progress: 0 }));
    const urls: string[] = [];
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": (url: string) => {
        urls.push(url);
        const q = new URL(url, "http://x").searchParams;
        const at = q.get("cursor") ? Number(q.get("cursor")!.replace("at-", "")) : 0;
        const items = all.slice(at, at + 3);
        return {
          items,
          page: 0,
          page_size: 3,
          total: at + items.length,
          has_more: at + 3 < all.length,
          next_cursor: `at-${at + 3}`,
        };
      },
    });
    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );
    await waitFor(() => expect(screen.getByText("Video number 8")).toBeTruthy());
    const followUps = urls.filter((u) => u.includes("/feeds/f1/videos")).slice(1);
    expect(followUps.length).toBeGreaterThan(0);
    for (const u of followUps) {
      expect(u).toContain("cursor=at-");
      expect(u).not.toContain("page=");
    }
  });

  it("stops observing once there is nothing left to load", async () => {
    useFakeObserver();
    mockFetch({
      "GET /api/v1/feeds": [feed()],
      "GET /api/v1/feeds/f1": feed(),
      "GET /api/v1/feeds/f1/videos": { items: [video()], page: 0, page_size: 30, total: 1 },
    });

    renderWithProviders(
      <Routes>
        <Route path="/feeds/:id" element={<FeedPage />} />
      </Routes>,
      { route: "/feeds/f1" },
    );

    await waitFor(() => expect(screen.getByText("The Beauty of Bézier Curves")).toBeTruthy());
    // A single page means no sentinel is left watching — otherwise every idle
    // feed keeps an observer alive for a page that will never come.
    await waitFor(() => expect(AsyncIntersectionObserver.live).toBe(0));
  });
});
