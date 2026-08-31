import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useSearchParams } from "react-router";
import type { SearchScope, SubtitleHit, VideoSummary } from "@/lib/api";
import { pinnedFeed, useFeeds, useSearch } from "@/lib/queries";
import { ccLabel, fmtDuration, plural } from "@/lib/format";
import { Avatar, EmptyState, ErrorState, LoadingState, MediaImg, SearchIcon } from "@/components/ui";
import { VideoRow } from "@/components/VideoRow";
import { trackSearch } from "@/lib/analytics";

const SCOPES: { value: SearchScope; label: string }[] = [
  { value: "all", label: "Everything" },
  { value: "titles", label: "Titles" },
  { value: "subtitles", label: "Subtitles" },
  { value: "channels", label: "Channels" },
  { value: "playlists", label: "Playlists" },
];

const PAGE = 5;

export default function SearchPage() {
  const [params, setParams] = useSearchParams();
  const q = params.get("q") ?? "";
  const scope = (params.get("scope") as SearchScope | null) ?? "all";
  const unseen = params.get("unseen") === "1";
  const feedParam = params.get("feed") ?? "";
  const [input, setInput] = useState(q);
  useEffect(() => setInput(q), [q]);

  // Debounce typing into the URL (?q=) so results are shareable/back-able.
  useEffect(() => {
    const t = setTimeout(() => {
      if (input.trim() !== q) {
        const next = new URLSearchParams(params);
        if (input.trim()) next.set("q", input.trim());
        else next.delete("q");
        setParams(next, { replace: true });
        // The scope only — the query itself is exactly what analytics must
        // never see.
        if (input.trim()) trackSearch(scope);
      }
    }, 300);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [input]);

  const set = (k: string, v: string | null) => {
    const next = new URLSearchParams(params);
    if (v) next.set(k, v);
    else next.delete(k);
    setParams(next, { replace: true });
  };

  // "Current feed" chip: the feed the user came from (state) or the pinned one.
  const feeds = useFeeds();
  const location = useLocation() as { state?: { feed?: string } };
  const currentFeed = useMemo(() => {
    const fromState = feeds.data?.find((f) => f.id === location.state?.feed);
    return fromState ?? pinnedFeed(feeds.data);
  }, [feeds.data, location.state]);

  const result = useSearch(q, scope, unseen, feedParam || undefined);
  const [showAll, setShowAll] = useState(false);
  useEffect(() => setShowAll(false), [q, scope, unseen, feedParam]);

  const r = result.data;
  const totalResults = r ? r.videos.total + r.channels.total + r.playlists.total : 0;
  const videos = r?.videos.items ?? [];
  const visibleVideos = showAll ? videos : videos.slice(0, PAGE);

  return (
    <div className="flex flex-col gap-4 px-5 pb-10 pt-[max(20px,env(safe-area-inset-top))] md:px-10 md:pt-8">
      <label className="flex items-center gap-3.5 rounded-2xl border-[1.5px] border-hair-2 bg-surface px-4 py-3 text-[18px] font-bold md:px-5 md:py-3.5 md:text-[20px] focus-within:border-accent">
        <SearchIcon size={22} />
        <input
          autoFocus
          type="search"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Escape") setInput("");
          }}
          placeholder="Search titles, subtitles, channels, playlists"
          className="w-full min-w-0 bg-transparent outline-none"
        />
        <span className="hidden text-[12px] font-semibold text-muted-2 md:inline">esc to clear</span>
      </label>
      <div className="flex items-center gap-2 overflow-x-auto no-scrollbar">
        {SCOPES.map((s) => (
          <button key={s.value} className={`seg ${scope === s.value ? "on" : ""}`} onClick={() => set("scope", s.value === "all" ? null : s.value)}>
            {s.label}
          </button>
        ))}
        <span className="mx-1 h-6 w-px flex-none bg-hair-2" />
        <button className={`seg ${unseen ? "on" : ""}`} onClick={() => set("unseen", unseen ? null : "1")}>
          Unseen
        </button>
        {currentFeed && (
          <button className={`seg ${feedParam === currentFeed.id ? "on" : ""}`} onClick={() => set("feed", feedParam === currentFeed.id ? null : currentFeed.id)}>
            {currentFeed.name} feed
          </button>
        )}
      </div>
      {/* Its own line rather than squeezed into the chip row, which clipped
          it (e.g. "2 results · 0.0") once the chips filled the width. */}
      {r && (
        <span className="meta whitespace-nowrap">
          {totalResults} results · {(r.took_ms / 1000).toFixed(2)} s
        </span>
      )}

      {!q ? (
        <EmptyState title="Search the archive" hint="Titles, channel and playlist names, and every word of the archived subtitles." />
      ) : result.isLoading ? (
        <LoadingState label="Searching…" />
      ) : result.isError ? (
        <ErrorState message={result.error.message} retry={() => result.refetch()} />
      ) : r && totalResults === 0 ? (
        <EmptyState title={`No results for “${q}”`} />
      ) : r ? (
        <div className="flex flex-col gap-8 lg:flex-row lg:gap-10">
          {r.videos.total > 0 && (
            <div className="flex min-w-0 flex-1 flex-col">
              <span className="sec pb-1">Videos · {r.videos.total}</span>
              {visibleVideos.map(({ video, subtitle_hits }) => (
                <SearchVideoRow key={video.id} video={video} hits={subtitle_hits} q={q} />
              ))}
              {!showAll && videos.length > PAGE && (
                <button className="py-3 text-left text-[13px] font-bold text-accent" onClick={() => setShowAll(true)}>
                  Show {videos.length - PAGE} more videos
                </button>
              )}
            </div>
          )}
          {(r.channels.total > 0 || r.playlists.total > 0) && (
            <div className="flex w-full flex-none flex-col gap-[22px] lg:w-[320px]">
              {r.channels.total > 0 && (
                <div className="flex flex-col gap-1.5">
                  <span className="sec pb-1">Channels · {r.channels.total}</span>
                  {r.channels.items.map((c) => (
                    <Link key={c.id} to={`/channels/${c.id}`} className="flex items-center gap-3 rounded-xl border border-hair bg-surface px-3 py-2.5 text-ink no-underline hover:text-ink">
                      <Avatar src={c.thumb_url} name={c.name} />
                      <span className="flex min-w-0 flex-col gap-0.5">
                        <span className="truncate text-[14px] font-extrabold">{c.name}</span>
                        <span className="meta text-[12px]">
                          {plural(c.video_count, "video")}
                          {c.match_count > 0 && ` · ${c.match_count} match`}
                        </span>
                      </span>
                    </Link>
                  ))}
                </div>
              )}
              {r.playlists.total > 0 && (
                <div className="flex flex-col gap-1.5">
                  <span className="sec pb-1">Playlists · {r.playlists.total}</span>
                  {r.playlists.items.map((p) => (
                    <Link key={p.id} to={`/playlists/${p.id}`} className="flex items-center gap-3 rounded-xl border border-hair bg-surface px-3 py-2.5 text-ink no-underline hover:text-ink">
                      <span className="relative h-9 w-16 flex-none overflow-hidden rounded-md bg-thumb">
                        {p.thumb_url && <MediaImg src={p.thumb_url} alt="" className="h-full w-full object-cover" />}
                      </span>
                      <span className="flex min-w-0 flex-col gap-0.5">
                        <span className="truncate text-[14px] font-extrabold">{p.name}</span>
                        <span className="meta text-[12px]">
                          {p.kind === "custom" ? "Yours" : p.channel?.name} · {plural(p.video_count, "video")}
                          {p.match_count > 0 && ` · ${p.match_count} match`}
                        </span>
                      </span>
                    </Link>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      ) : null}
    </div>
  );
}

function SearchVideoRow({ video, hits, q }: { video: VideoSummary; hits: SubtitleHit[]; q: string }) {
  const inProgress = !video.watched && video.position > 0;
  const status = video.watched ? "seen" : inProgress ? `stopped at ${fmtDuration(video.position)}` : ccLabel(video.subtitle_langs, video.has_auto_subtitles);
  return (
    <VideoRow
      video={video}
      thumbWidth={168}
      meta={
        <>
          <Link to={`/channels/${video.channel.id}`} className="!font-medium !text-muted">{video.channel.name}</Link> · {fmtDuration(video.duration)} · {status}
        </>
      }
      extra={
        hits.length > 0 && (
          <div className="mt-1 flex flex-col gap-1">
            {hits.slice(0, 3).map((h, i) => (
              <Link
                key={i}
                to={`/watch/${video.id}?t=${Math.floor(h.start)}`}
                className="flex items-center gap-2 rounded-lg bg-raised-2 px-2.5 py-1.5 text-[13px] font-medium text-ink-2 no-underline hover:bg-raised"
              >
                <svg className="flex-none" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="3" y="6" width="18" height="12" rx="2" /><path d="M7 12h4M13 12h4" /></svg>
                <span className="line-clamp-2">
                  <span className="font-bold text-accent">{fmtDuration(h.start)}</span> <Highlight text={h.text} q={q} />
                </span>
              </Link>
            ))}
          </div>
        )
      }
    />
  );
}

// Wraps case-insensitive matches of the query in <mark>.
export function Highlight({ text, q }: { text: string; q: string }) {
  const term = q.trim();
  if (!term) return <>{text}</>;
  const re = new RegExp(`(${term.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`, "ig");
  const parts = text.split(re);
  return (
    <>
      {parts.map((p, i) => (p.toLowerCase() === term.toLowerCase() ? <mark key={i}>{p}</mark> : <span key={i}>{p}</span>))}
    </>
  );
}
