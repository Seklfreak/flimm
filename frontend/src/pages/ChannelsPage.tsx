import { useState } from "react";
import { Link } from "react-router";
import { EVERYTHING_ID, type ChannelSort, type ChannelSummary } from "@/lib/api";
import { useChannels } from "@/lib/queries";
import { plural, relativeDay } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { Avatar, EmptyState, ErrorState, InfiniteSentinel, SearchBox, Spinner } from "@/components/ui";

type Mode = "recent" | "az" | "unfeeded";

export default function ChannelsPage() {
  const [q, setQ] = useState("");
  const [mode, setMode] = useState<Mode>("recent");
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
          </>
        }
      />
      <div className="px-5 md:px-10">
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
