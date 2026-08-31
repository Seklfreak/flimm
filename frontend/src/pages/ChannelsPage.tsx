import { useState } from "react";
import { Link } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, EVERYTHING_ID, type ChannelSort, type ChannelSummary } from "@/lib/api";
import { useChannels, useMe } from "@/lib/queries";
import { plural, relativeDay } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { Avatar, EmptyState, ErrorState, InfiniteSentinel, SearchBox, Spinner } from "@/components/ui";

type Mode = "recent" | "az" | "unfeeded";

export default function ChannelsPage() {
  const [q, setQ] = useState("");
  const [mode, setMode] = useState<Mode>("recent");
  const me = useMe();
  const [adding, setAdding] = useState(false);
  const sort: ChannelSort = mode === "az" ? "name" : "last_upload";
  const channels = useChannels(q.trim(), sort, mode === "unfeeded");
  const items = channels.data?.pages.flatMap((p) => p.items) ?? [];
  const total = channels.data?.pages[0]?.total;

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-[22px]">
      <PageHeader
        title="Channels"
        meta={total !== undefined ? `${total} subscribed` : undefined}
        actions={
          <>
            <SearchBox value={q} onChange={setQ} placeholder="Filter channels" className="w-[220px]" />
            {(
              [
                ["recent", "Recent"],
                ["az", "A–Z"],
                ["unfeeded", "Not in any feed"],
              ] as [Mode, string][]
            ).map(([m, label]) => (
              <button key={m} className={`seg ${mode === m ? "on" : ""}`} onClick={() => setMode(m)}>
                {label}
              </button>
            ))}
            {me.data?.is_admin && (
              <button className="btn pri ml-2" onClick={() => setAdding(true)}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M12 5v14M5 12h14" /></svg>
                Add channel
              </button>
            )}
          </>
        }
      />
      <div className="px-5 md:px-10">
        {adding && <AddChannelForm onDone={() => setAdding(false)} />}
        {channels.isLoading ? (
          <Spinner label="Loading channels…" />
        ) : channels.isError ? (
          <ErrorState message={channels.error.message} retry={() => channels.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState title={mode === "unfeeded" ? "Every channel is in a feed" : "No channels"} />
        ) : (
          <>
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              {items.map((c) => (
                <ChannelCard key={c.id} channel={c} />
              ))}
            </div>
            <InfiniteSentinel enabled={!!channels.hasNextPage && !channels.isFetchingNextPage} onVisible={() => void channels.fetchNextPage()} />
            {channels.isFetchingNextPage && <div className="py-6"><Spinner /></div>}
          </>
        )}
      </div>
    </div>
  );
}

function ChannelCard({ channel }: { channel: ChannelSummary }) {
  const feeds = channel.feeds.filter((f) => f.id !== EVERYTHING_ID);
  return (
    <Link
      to={`/channels/${channel.id}`}
      className="flex items-center gap-4 rounded-[14px] border border-hair bg-surface px-3 py-3.5 text-ink no-underline hover:border-hair-2 hover:text-ink"
    >
      <Avatar src={channel.thumb_url} name={channel.name} />
      <span className="flex min-w-0 flex-1 flex-col gap-0.5">
        <span className="truncate text-[15px] font-extrabold">{channel.name}</span>
        <span className="meta">
          {plural(channel.video_count, "video")}
          {channel.last_upload && ` · latest ${relativeDay(channel.last_upload)}`}
        </span>
      </span>
      <span className="hidden flex-wrap gap-1.5 sm:flex">
        {feeds.length > 0 ? feeds.map((f) => <span key={f.id} className="chip">{f.name}</span>) : <span className="meta text-[12px]">not in a feed</span>}
      </span>
      {channel.unseen_count > 0 && <span className="badge whitespace-nowrap">{channel.unseen_count} unseen</span>}
    </Link>
  );
}

/**
 * Admin only: hand TubeArchivist a channel it may not know yet — a URL,
 * @handle or UC… id. TA resolves and creates it in a background task, so the
 * channel appears in the directory once that lands, not on submit.
 */
function AddChannelForm({ onDone }: { onDone: () => void }) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [requested, setRequested] = useState(false);
  const qc = useQueryClient();
  const submit = async () => {
    if (!value.trim() || busy) return;
    setBusy(true);
    try {
      await api.subscribeNewChannel(value.trim());
      setRequested(true);
      void qc.invalidateQueries({ queryKey: ["channels"] });
    } finally {
      setBusy(false);
    }
  };
  if (requested) {
    return (
      <div className="mb-4 flex flex-wrap items-center gap-3 rounded-[14px] border border-hair bg-raised/60 px-4 py-3">
        <span className="meta">
          Asked the archive to subscribe. TubeArchivist resolves and downloads in the background — the channel appears in the directory once that lands.
        </span>
        <button className="btn" onClick={onDone}>OK</button>
      </div>
    );
  }
  return (
    <form
      className="mb-4 flex flex-wrap items-center gap-2 rounded-[14px] border border-hair bg-raised/60 px-4 py-3"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <input
        className="input min-w-[280px] flex-1"
        value={value}
        autoFocus
        onChange={(e) => setValue(e.target.value)}
        placeholder="Channel URL, @handle or UC… id"
      />
      <button className="btn pri" type="submit" disabled={busy || !value.trim()}>
        {busy ? "Subscribing…" : "Subscribe"}
      </button>
      <button className="btn" type="button" onClick={onDone}>
        Cancel
      </button>
    </form>
  );
}
