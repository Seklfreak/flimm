import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api, type PlaylistSummary } from "@/lib/api";
import { usePlaylists, useSetPlaylistPinned } from "@/lib/queries";
import { plural } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { EmptyState, ErrorState, HeadphonesIcon, InfiniteSentinel, MediaImg, PinIcon, ProgressBar, Segmented, Spinner } from "@/components/ui";
import { VideoGrid } from "@/components/VideoCard";

type Filter = "all" | "custom" | "channel";

export default function PlaylistsPage() {
  const [filter, setFilter] = useState<Filter>("all");
  const lists = usePlaylists(filter === "all" ? undefined : filter);
  const items = lists.data?.pages.flatMap((p) => p.items) ?? [];
  const mine = items.filter((p) => p.kind === "custom");
  const fromChannels = items.filter((p) => p.kind === "channel");
  const [creating, setCreating] = useState(false);

  return (
    <div className="flex flex-col gap-4 pb-10 md:gap-[22px]">
      <PageHeader
        title="Playlists"
        meta={lists.data && `${mine.length} yours · ${fromChannels.length} from channels`}
        actions={
          <>
            <Segmented
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: "All" },
                { value: "custom", label: "Mine" },
                { value: "channel", label: "From channels" },
              ]}
            />
            <button className="btn pri ml-2" onClick={() => setCreating(true)}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M12 5v14M5 12h14" /></svg>
              New playlist
            </button>
          </>
        }
      />
      <div className="flex flex-col gap-5 px-5 md:px-10">
        {creating && <NewPlaylistForm onDone={() => setCreating(false)} />}
        {lists.isLoading ? (
          <Spinner label="Loading playlists…" />
        ) : lists.isError ? (
          <ErrorState message={lists.error.message} retry={() => lists.refetch()} />
        ) : items.length === 0 ? (
          <EmptyState title="No playlists yet" hint="Create one, or add videos to a playlist from the player." />
        ) : (
          <>
            {mine.length > 0 && (
              <section className="flex flex-col gap-4">
                <span className="sec">Yours</span>
                <VideoGrid>{mine.map((p) => <PlaylistCard key={p.id} playlist={p} />)}</VideoGrid>
              </section>
            )}
            {fromChannels.length > 0 && (
              <section className="flex flex-col gap-4">
                <span className="sec">From channels</span>
                <VideoGrid>{fromChannels.map((p) => <PlaylistCard key={p.id} playlist={p} />)}</VideoGrid>
              </section>
            )}
            <InfiniteSentinel enabled={!!lists.hasNextPage && !lists.isFetchingNextPage} onVisible={() => void lists.fetchNextPage()} />
          </>
        )}
      </div>
    </div>
  );
}

function NewPlaylistForm({ onDone }: { onDone: () => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const qc = useQueryClient();
  const navigate = useNavigate();
  const submit = async () => {
    if (!name.trim()) return;
    setBusy(true);
    try {
      const p = await api.createPlaylist(name.trim());
      await qc.invalidateQueries({ queryKey: ["playlists"] });
      onDone();
      navigate(`/playlists/${p.id}`);
    } finally {
      setBusy(false);
    }
  };
  return (
    <form
      className="flex items-center gap-2"
      onSubmit={(e) => {
        e.preventDefault();
        void submit();
      }}
    >
      <input className="input max-w-sm" autoFocus placeholder="Playlist name" value={name} onChange={(e) => setName(e.target.value)} />
      <button className="btn pri" type="submit" disabled={busy || !name.trim()}>Create</button>
      <button className="btn" type="button" onClick={onDone}>Cancel</button>
    </form>
  );
}

// Stacked-card thumbnail with count + progress (Playlists artboard).
export function PlaylistStack({ playlist, compact = false }: { playlist: PlaylistSummary; compact?: boolean }) {
  return (
    <div className="relative aspect-video w-full">
      <div className="absolute inset-x-3 top-0 h-3 rounded-t-xl bg-av opacity-70" />
      <div className="absolute inset-x-1.5 top-1.5 h-3 rounded-t-xl bg-av" />
      <div className="absolute inset-x-0 bottom-0 top-3 overflow-hidden rounded-2xl bg-thumb">
        {playlist.thumb_url && <MediaImg src={playlist.thumb_url} alt="" className="absolute inset-0 h-full w-full object-cover" />}
        {!compact && (
          <span className="pill bottom-3 right-3 flex items-center gap-1.5">
            <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M4 6h12M4 12h12M4 18h8" /><path d="M19 14l3 2-3 2z" fill="currentColor" /></svg>
            {playlist.video_count}
          </span>
        )}
        {/* A music playlist reports no watch progress to show (see docs/api.md
            "Music playlists"). */}
        {!playlist.music && playlist.progress > 0 && <ProgressBar value={playlist.progress} className="absolute inset-x-3 bottom-3" />}
      </div>
    </div>
  );
}

export function PlaylistCard({ playlist }: { playlist: PlaylistSummary }) {
  const seen =
    playlist.music || playlist.seen_count === 0
      ? ""
      : playlist.seen_count >= playlist.video_count
        ? " · all seen"
        : ` · ${playlist.seen_count} seen`;
  const setPinned = useSetPlaylistPinned();
  return (
    <div className="relative flex flex-col gap-2.5">
      <Link to={`/playlists/${playlist.id}`} className="flex flex-col gap-2.5 text-ink no-underline hover:text-ink">
        <PlaylistStack playlist={playlist} />
        <span className="flex flex-col gap-0.5">
          <span className="flex items-center gap-1.5 text-[16px] font-extrabold leading-[1.25] tracking-[-0.01em]">
            <span className="line-clamp-2">{playlist.name}</span>
            {playlist.music && (
              <span className="flex-none text-muted-2" title="Music playlist: audio only, no watch history">
                <HeadphonesIcon size={13} />
              </span>
            )}
          </span>
          <span className="meta">
            {playlist.channel ? `${playlist.channel.name} · ` : ""}
            {plural(playlist.video_count, "video")}
            {seen}
          </span>
        </span>
      </Link>
      {/* Sits above the Link (not nested inside it) so pinning never triggers navigation. */}
      <button
        type="button"
        className={`pill right-2.5 top-2.5 flex items-center justify-center !p-1.5 ${playlist.pinned ? "!bg-accent !text-white" : "hover:!text-white"}`}
        aria-pressed={playlist.pinned}
        aria-label={playlist.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
        title={playlist.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
        onClick={(e) => {
          e.preventDefault();
          e.stopPropagation();
          setPinned.mutate({ id: playlist.id, pinned: !playlist.pinned });
        }}
      >
        <PinIcon size={13} />
      </button>
    </div>
  );
}
