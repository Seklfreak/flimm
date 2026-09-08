import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, ApiError, EVERYTHING_ID, type ChannelSort, type ChannelSummary } from "@/lib/api";
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
 * @handle or UC… id. The request holds while TA resolves and creates it, so
 * the form waits on it and opens the channel when it lands; only when TA is
 * still at it past the server's patience does it fall back to "appears
 * later". A failure — a handle that does not resolve, a URL off youtube.com —
 * comes back with TA's reason instead of vanishing into the archive's logs.
 */
function AddChannelForm({ onDone }: { onDone: () => void }) {
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const submit = async () => {
    if (!value.trim() || busy) return;
    setBusy(true);
    setError(null);
    try {
      const result = await api.subscribeNewChannel(value.trim());
      void qc.invalidateQueries({ queryKey: ["channels"] });
      if (result.status === "added") {
        void navigate(`/channels/${result.channel.id}`);
        return;
      }
      setPending(true);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Could not reach the server.");
    } finally {
      setBusy(false);
    }
  };
  if (pending) {
    return (
      <div className="mb-4 flex flex-wrap items-center gap-3 rounded-[14px] border border-hair bg-raised/60 px-4 py-3">
        <span className="meta">
          TubeArchivist is still resolving the channel. It appears in the directory once that lands.
        </span>
        <button className="btn" onClick={onDone}>OK</button>
      </div>
    );
  }
  if (busy) {
    return (
      <div className="mb-4 flex flex-wrap items-center gap-3 rounded-[14px] border border-hair bg-raised/60 px-4 py-3">
        <Spinner label="Waiting for TubeArchivist to resolve and index the channel…" />
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
      <button className="btn pri" type="submit" disabled={!value.trim()}>
        Subscribe
      </button>
      <button className="btn" type="button" onClick={onDone}>
        Cancel
      </button>
      {error && <span className="meta w-full text-danger">{error}</span>}
    </form>
  );
}
