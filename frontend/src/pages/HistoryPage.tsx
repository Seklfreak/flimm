import { useEffect, useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, type HistoryEntry, type HistoryFilter } from "@/lib/api";
import { useHistory } from "@/lib/queries";
import { dayHeading, dayKey, fmtClock, fmtDuration } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { CheckIcon, EmptyState, ErrorState, InfiniteSentinel, SearchBox, Segmented, Spinner } from "@/components/ui";
import { VideoRow } from "@/components/VideoRow";
import { watchHref } from "@/components/VideoCard";

export default function HistoryPage() {
  const [filter, setFilter] = useState<HistoryFilter>("all");
  const [q, setQ] = useState("");
  const [debounced, setDebounced] = useState("");
  useEffect(() => {
    const t = setTimeout(() => setDebounced(q.trim()), 250);
    return () => clearTimeout(t);
  }, [q]);
  const history = useHistory(filter, debounced);
  const qc = useQueryClient();
  const entries = history.data?.pages.flatMap((p) => p.items) ?? [];
  const inProgress = entries.filter((e) => e.state === "in_progress").length;

  const remove = async (id: string) => {
    await api.deleteHistory(id);
    void qc.invalidateQueries({ queryKey: ["history"] });
  };

  // Group by local day, preserving newest-first order.
  const groups: { key: string; heading: string; items: HistoryEntry[] }[] = [];
  for (const e of entries) {
    const key = dayKey(e.played_at);
    const g = groups[groups.length - 1];
    if (g && g.key === key) g.items.push(e);
    else groups.push({ key, heading: dayHeading(e.played_at), items: [e] });
  }

  return (
    <div className="flex flex-col gap-3 pb-10 md:gap-2.5">
      <PageHeader
        title="History"
        meta={history.data ? `${inProgress} in progress` : undefined}
        actions={
          <>
            <Segmented
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: "All" },
                { value: "in_progress", label: "In progress" },
                { value: "seen", label: "Seen" },
              ]}
            />
            <SearchBox value={q} onChange={setQ} placeholder="Search history" className="ml-2 w-[200px]" />
          </>
        }
      />
      <div className="flex flex-col px-5 md:px-10">
        {history.isLoading ? (
          <Spinner label="Loading history…" />
        ) : history.isError ? (
          <ErrorState message={history.error.message} retry={() => history.refetch()} />
        ) : entries.length === 0 ? (
          <EmptyState title="Nothing here yet" hint={debounced ? "No history entries match." : "Videos you play show up here."} />
        ) : (
          <>
            {groups.map((g, gi) => (
              <section key={g.key} className="flex flex-col">
                <span className={`sec ${gi === 0 ? "pb-1" : "pb-1 pt-4"}`}>{g.heading}</span>
                {g.items.map((e) => (
                  <HistoryRow key={e.id} entry={e} onRemove={() => void remove(e.id)} />
                ))}
              </section>
            ))}
            <InfiniteSentinel enabled={!!history.hasNextPage && !history.isFetchingNextPage} onVisible={() => void history.fetchNextPage()} />
            {history.isFetchingNextPage && <div className="py-6"><Spinner /></div>}
          </>
        )}
      </div>
    </div>
  );
}

function HistoryRow({ entry, onRemove }: { entry: HistoryEntry; onRemove: () => void }) {
  const v = entry.video;
  const resumable = entry.state === "in_progress";
  // Resume into the feed the video lives in (see docs/api.md HistoryEntry).
  const ctx = entry.feed ? { feed: entry.feed.id } : undefined;
  return (
    <VideoRow
      video={v}
      ctx={ctx}
      lead={<span className="meta hidden w-14 flex-none text-[12px] md:inline">{fmtClock(entry.played_at)}</span>}
      meta={
        <>
          <Link to={`/channels/${v.channel.id}`} className="!font-medium !text-muted">{v.channel.name}</Link> · {fmtDuration(v.duration)}
          {resumable && ` · stopped at ${fmtDuration(v.position)}`}
        </>
      }
      actions={
        <>
          {resumable ? (
            <Link to={watchHref(v, ctx)} className="btn pri no-underline">
              <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M7 4l12 8-12 8z" /></svg>
              <span className="hidden sm:inline">Resume</span>
            </Link>
          ) : (
            <Link to={watchHref(v, ctx)} className="btn no-underline">
              <CheckIcon size={13} />
              <span className="hidden sm:inline">Seen</span>
            </Link>
          )}
          <button className="text-muted-3 hover:text-danger" aria-label="Remove from history" onClick={onRemove}>
            <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M6 6l12 12M18 6L6 18" /></svg>
          </button>
        </>
      }
    />
  );
}
