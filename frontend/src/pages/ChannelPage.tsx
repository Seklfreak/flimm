import { useState } from "react";
import { Link, useParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, EVERYTHING_ID, type PlaylistSummary } from "@/lib/api";
import { invalidateFeedish, invalidateWatchState, useChannel, useChannelPlaylists, useChannelVideos, useFeeds } from "@/lib/queries";
import { plural, relativeDay } from "@/lib/format";
import { Avatar, CheckIcon, EmptyState, ErrorState, InfiniteSentinel, Popover, Segmented, Spinner } from "@/components/ui";
import { VideoCard, VideoGrid } from "@/components/VideoCard";
import { PlaylistCard } from "@/pages/PlaylistsPage";

type Tab = "all" | "unseen" | "playlists";

export default function ChannelPage() {
  const { id = "" } = useParams();
  const channel = useChannel(id);
  const [tab, setTab] = useState<Tab>("all");
  const videos = useChannelVideos(id, tab === "unseen" ? "unseen" : "all");
  const playlists = useChannelPlaylists(id, tab === "playlists");
  const qc = useQueryClient();
  const [marking, setMarking] = useState(false);

  if (channel.isError) return <ErrorState message={channel.error.message} retry={() => channel.refetch()} />;
  const c = channel.data;
  const items = videos.data?.pages.flatMap((p) => p.items) ?? [];

  const markAll = async () => {
    setMarking(true);
    try {
      await api.markChannelSeen(id);
      invalidateWatchState(qc);
      void qc.invalidateQueries({ queryKey: ["channels", id] });
    } finally {
      setMarking(false);
    }
  };

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-6">
      <div className="flex flex-col gap-4 px-5 pt-[max(20px,env(safe-area-inset-top))] md:gap-6 md:px-10 md:pt-8">
        <div className="flex items-center gap-2 text-[13px] font-semibold text-muted-2">
          <Link to="/channels" className="text-muted-2 no-underline hover:text-ink">Channels</Link>
          <span>/</span>
          <span className="truncate text-ink">{c?.name ?? "…"}</span>
        </div>
        <div className="flex flex-wrap items-center gap-4 md:gap-5">
          <Avatar src={c?.thumb_url} name={c?.name ?? "?"} size={72} />
          <div className="flex min-w-0 flex-1 flex-col gap-1">
            <span className="h1 truncate">{c?.name ?? "…"}</span>
            {c && (
              <span className="meta">
                {plural(c.video_count, "video")} · {c.unseen_count} unseen
                {c.last_upload && ` · last upload ${relativeDay(c.last_upload)}`}
              </span>
            )}
          </div>
          {c && (
            <div className="flex items-center gap-2">
              <InFeedsControl channelId={c.id} feedIds={c.feeds.map((f) => f.id)} />
              <button className="seg" onClick={() => void markAll()} disabled={marking || c.unseen_count === 0}>
                {marking ? "Marking…" : "Mark all seen"}
              </button>
            </div>
          )}
        </div>
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: "all", label: "All videos" },
            { value: "unseen", label: "Unseen" },
            { value: "playlists", label: "Playlists" },
          ]}
        />
      </div>
      <div className="px-5 md:px-10">
        {tab === "playlists" ? (
          playlists.isLoading ? (
            <Spinner label="Loading playlists…" />
          ) : (playlists.data ?? []).length === 0 ? (
            <EmptyState title="No playlists archived from this channel" />
          ) : (
            <VideoGrid>
              {(playlists.data ?? []).map((p: PlaylistSummary) => (
                <PlaylistCard key={p.id} playlist={p} />
              ))}
            </VideoGrid>
          )
        ) : videos.isLoading ? (
          <Spinner label="Loading videos…" />
        ) : videos.isError ? (
          <ErrorState message={videos.error.message} retry={() => videos.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState title={tab === "unseen" ? "All caught up" : "No videos archived yet"} />
        ) : (
          <>
            <VideoGrid>
              {items.map((v) => (
                <VideoCard key={v.id} video={v} ctx={{ channel: id }} showChannel={false} />
              ))}
            </VideoGrid>
            <InfiniteSentinel enabled={!!videos.hasNextPage && !videos.isFetchingNextPage} onVisible={() => void videos.fetchNextPage()} />
            {videos.isFetchingNextPage && <div className="py-6"><Spinner /></div>}
          </>
        )}
      </div>
    </div>
  );
}

// "In feeds: Home, DevOps ▾" — multi-select popover → PUT /channels/:id/feeds.
function InFeedsControl({ channelId, feedIds }: { channelId: string; feedIds: string[] }) {
  const feeds = useFeeds();
  const qc = useQueryClient();
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  const [saving, setSaving] = useState(false);
  const custom = (feeds.data ?? []).filter((f) => f.id !== EVERYTHING_ID);
  const selected = new Set(feedIds.filter((id) => id !== EVERYTHING_ID));
  const label = custom.filter((f) => selected.has(f.id)).map((f) => f.name);

  const toggle = async (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSaving(true);
    try {
      await api.setChannelFeeds(channelId, [...next]);
      await qc.invalidateQueries({ queryKey: ["channels", channelId] });
      invalidateFeedish(qc);
    } finally {
      setSaving(false);
    }
  };

  return (
    <>
      <button
        ref={setAnchor}
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="listbox"
        aria-expanded={open}
        className="flex items-center gap-2 rounded-full border-[1.5px] border-hair-2 px-3.5 py-[9px] text-[13px] font-bold"
      >
        <span className="max-w-[220px] truncate">In feeds: {label.length > 0 ? label.join(", ") : "none"}</span>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M6 9l6 6 6-6" /></svg>
      </button>
      {open && (
        <Popover anchor={anchor} onClose={() => setOpen(false)}>
          <div className="pop" role="listbox" aria-multiselectable>
            {custom.length === 0 && (
              <Link to="/feeds/new" className="pop-item text-white no-underline">New feed…</Link>
            )}
            {custom.map((f) => (
              <button key={f.id} role="option" aria-selected={selected.has(f.id)} className={`pop-item ${selected.has(f.id) ? "on" : ""}`} onClick={() => void toggle(f.id)} disabled={saving}>
                <span>{f.name}</span>
                {selected.has(f.id) && <CheckIcon size={14} />}
              </button>
            ))}
          </div>
        </Popover>
      )}
    </>
  );
}
