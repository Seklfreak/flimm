import { useMemo, useState } from "react";
import { useNavigate } from "react-router";
import { EVERYTHING_ID, type ChannelSummary, type Feed, type FeedInput, type FeedSort } from "@/lib/api";
import { useAllChannels, useChannelPlaylists, useDeleteFeed, useSaveFeed, useUpdatePrefs, usePrefs } from "@/lib/queries";
import { plural } from "@/lib/format";
import { trackFeedCreated } from "@/lib/analytics";
import { Avatar, CheckIcon, Modal, SearchBox, Segmented, Spinner, Toggle } from "./ui";

const SORTS: { value: FeedSort; label: string }[] = [
  { value: "newest", label: "Newest" },
  { value: "oldest", label: "Oldest" },
  { value: "shortest", label: "Shortest" },
  { value: "longest", label: "Longest" },
];

// Same form for New feed and Edit feed (FeedEditor artboard). Centered modal on
// desktop, full-height sheet on mobile (Modal handles both). For the built-in
// Everything feed only the sort / hide-seen / shorts options are editable and
// they live in prefs.
export function FeedEditor({ feed, onClose }: { feed?: Feed; onClose: () => void }) {
  const navigate = useNavigate();
  const isEverything = feed?.id === EVERYTHING_ID;
  const prefs = usePrefs();
  const updatePrefs = useUpdatePrefs();
  const save = useSaveFeed();
  const del = useDeleteFeed();
  const channels = useAllChannels();

  const [name, setName] = useState(feed?.name ?? "");
  const [selected, setSelected] = useState<Set<string>>(() => new Set(feed?.channel_ids ?? []));
  // Playlist sources — single series picked from a channel's disclosure row.
  const [selectedSeries, setSelectedSeries] = useState<Set<string>>(() => new Set(feed?.playlist_ids ?? []));
  const [sort, setSort] = useState<FeedSort>(isEverything ? (prefs?.everything_sort ?? "newest") : (feed?.sort ?? "newest"));
  const [hideSeen, setHideSeen] = useState(isEverything ? (prefs?.everything_hide_seen ?? true) : (feed?.hide_seen ?? true));
  const [shorts, setShorts] = useState(isEverything ? (prefs?.everything_include_shorts ?? false) : (feed?.include_shorts ?? false));
  const [subsOnly, setSubsOnly] = useState(feed?.subtitles_only ?? false);
  const [pinned, setPinned] = useState(feed?.pinned ?? false);
  const [filter, setFilter] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const all = useMemo(() => channels.data ?? [], [channels.data]);
  const visible = useMemo(() => {
    const q = filter.trim().toLowerCase();
    return q ? all.filter((c) => c.name.toLowerCase().includes(q)) : all;
  }, [all, filter]);

  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });
  const toggleSeries = (id: string) =>
    setSelectedSeries((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const canSave = isEverything || (name.trim().length > 0 && selected.size + selectedSeries.size > 0);

  const onSave = async () => {
    setError(null);
    try {
      if (isEverything) {
        await updatePrefs.mutateAsync({ everything_sort: sort, everything_hide_seen: hideSeen, everything_include_shorts: shorts });
        onClose();
        return;
      }
      const input: FeedInput = {
        name: name.trim(),
        channel_ids: [...selected],
        playlist_ids: [...selectedSeries],
        sort,
        hide_seen: hideSeen,
        include_shorts: shorts,
        subtitles_only: subsOnly,
        pinned,
      };
      const saved = await save.mutateAsync({ id: feed?.id, input });
      if (feed) onClose();
      else {
        trackFeedCreated();
        navigate(`/feeds/${saved.id}`, { replace: true });
      }
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const onDelete = async () => {
    if (!feed) return;
    try {
      await del.mutateAsync(feed.id);
      navigate("/", { replace: true });
    } catch (e) {
      setError((e as Error).message);
    }
  };

  const busy = save.isPending || del.isPending || updatePrefs.isPending;

  return (
    // The Everything feed only ever shows the single 360px options column
    // (no channel picker), so it doesn't need the two-column New/Edit width.
    <Modal onClose={onClose} label={feed ? "Edit feed" : "New feed"} width={isEverything ? 480 : 920}>
      <div className="flex flex-none items-center justify-between border-b border-hair px-5 py-4 md:px-7 md:py-[22px]">
        <div className="flex items-center gap-3">
          <span className="text-[20px] font-extrabold tracking-[-0.02em]">{feed ? (isEverything ? "Feed options" : "Edit feed") : "New feed"}</span>
          {feed && <span className="meta">{feed.name}</span>}
        </div>
        <div className="flex gap-2">
          <button className="btn" onClick={onClose} disabled={busy}>
            Cancel
          </button>
          <button className="btn pri" onClick={() => void onSave()} disabled={!canSave || busy}>
            {busy ? "Saving…" : "Save"}
          </button>
        </div>
      </div>
      {error && <p className="px-7 pt-3 text-sm font-semibold text-danger">{error}</p>}
      <div className="flex min-h-0 flex-1 flex-col overflow-y-auto md:flex-row md:overflow-hidden">
        {/* `flex-1` + `min-h-0` on the picker column only from md up, where it
            is a real column that scrolls on its own. Below that the whole body
            is one scroller (the parent), and letting this column shrink below
            its content there — with nothing clipping or scrolling it — spilled
            the channel rows over the Options section underneath. */}
        {!isEverything && (
          <div className="flex flex-col gap-3.5 px-5 py-5 md:min-h-0 md:flex-1 md:overflow-hidden md:border-r md:border-hair md:px-7">
            <label className="flex flex-col gap-1.5">
              <span className="sec">Name</span>
              <input className="input" value={name} onChange={(e) => setName(e.target.value)} placeholder="e.g. DevOps" autoFocus />
            </label>
            <div className="flex items-center justify-between">
              <span className="sec">
                Channels · {selected.size} of {all.length}
                {selectedSeries.size > 0 && <> · Series · {selectedSeries.size}</>}
              </span>
              <div className="flex gap-2.5 text-[12px] font-bold">
                <button className="text-accent" onClick={() => setSelected(new Set(all.map((c) => c.id)))}>
                  Select all
                </button>
                <button
                  className="text-muted-2"
                  onClick={() => {
                    setSelected(new Set());
                    setSelectedSeries(new Set());
                  }}
                >
                  Clear
                </button>
              </div>
            </div>
            <SearchBox value={filter} onChange={setFilter} placeholder="Filter channels" className="!rounded-[10px]" />
            <div className="flex min-h-[200px] flex-col md:min-h-0 md:flex-1 md:overflow-y-auto">
              {channels.isLoading ? (
                <Spinner label="Loading channels…" />
              ) : (
                visible.map((c) => (
                  <ChannelPickRow
                    key={c.id}
                    channel={c}
                    selected={selected.has(c.id)}
                    onToggle={() => toggle(c.id)}
                    currentFeedId={feed?.id}
                    selectedSeries={selectedSeries}
                    onToggleSeries={toggleSeries}
                  />
                ))
              )}
              {!channels.isLoading && visible.length === 0 && <p className="meta py-4">No channels match.</p>}
            </div>
          </div>
        )}
        <div className="flex w-full flex-none flex-col justify-between gap-6 px-5 py-5 md:w-[360px] md:px-7">
          <div className="flex flex-col gap-1">
            <span className="sec pb-1.5">Options</span>
            <OptionRow title="Sort" hint="Order of the feed">
              <Segmented value={sort} onChange={setSort} options={SORTS} size="sm" />
            </OptionRow>
            <OptionRow title="Hide seen videos" hint="Default view shows unseen only">
              <Toggle on={hideSeen} onChange={setHideSeen} label="Hide seen videos" />
            </OptionRow>
            <OptionRow title="Include Shorts" hint="Show videos under a minute">
              <Toggle on={shorts} onChange={setShorts} label="Include Shorts" />
            </OptionRow>
            {!isEverything && (
              <>
                <OptionRow title="Only with subtitles" hint="Skip videos without a track">
                  <Toggle on={subsOnly} onChange={setSubsOnly} label="Only with subtitles" />
                </OptionRow>
                <OptionRow title="Pin to top" hint="The feed the app opens on">
                  <Toggle on={pinned} onChange={setPinned} label="Pin to top" />
                </OptionRow>
              </>
            )}
          </div>
          {feed && !isEverything && (
            <div className="flex flex-col gap-2">
              {confirmDelete ? (
                <div className="flex flex-wrap items-center gap-2 text-[13px] font-semibold">
                  <span className="meta">Delete “{feed.name}”? Channels and videos stay.</span>
                  <button className="btn !bg-danger !text-white" onClick={() => void onDelete()} disabled={busy}>
                    Delete
                  </button>
                  <button className="btn" onClick={() => setConfirmDelete(false)}>
                    Keep
                  </button>
                </div>
              ) : (
                <button className="btn danger" onClick={() => setConfirmDelete(true)}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13" /></svg>
                  Delete feed
                </button>
              )}
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}

function OptionRow({ title, hint, children }: { title: string; hint: string; children: React.ReactNode }) {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-hair py-2.5">
      <div className="flex flex-col gap-0.5">
        <span className="text-[14px] font-bold">{title}</span>
        <span className="meta text-[12px]">{hint}</span>
      </div>
      {children}
    </div>
  );
}

function ChannelPickRow({
  channel,
  selected,
  onToggle,
  currentFeedId,
  selectedSeries,
  onToggleSeries,
}: {
  channel: ChannelSummary;
  selected: boolean;
  onToggle: () => void;
  currentFeedId?: string;
  selectedSeries: Set<string>;
  onToggleSeries: (id: string) => void;
}) {
  const [showSeries, setShowSeries] = useState(false);
  // Fetched only once the disclosure is opened, so the picker costs nothing
  // for the channels nobody expands.
  const series = useChannelPlaylists(channel.id, showSeries);
  const others = channel.feeds.filter((f) => f.id !== currentFeedId && f.id !== EVERYTHING_ID);
  const hint = others.length > 0 ? `also in ${others.map((f) => f.name).join(", ")}` : "not in a feed";
  const pickedHere = (series.data ?? []).filter((p) => selectedSeries.has(p.id)).length;
  return (
    <div className="flex flex-col">
      <div className="flex items-center gap-1">
        <button
          type="button"
          role="checkbox"
          aria-checked={selected}
          onClick={onToggle}
          className="flex min-w-0 flex-1 items-center gap-3 rounded-[10px] px-2 py-2.5 text-left hover:bg-raised/70"
        >
          <span className={`flex h-[22px] w-[22px] flex-none items-center justify-center rounded-[7px] text-white ${selected ? "bg-accent" : "border-[1.5px] border-hair-2"}`}>
            {selected && <CheckIcon size={12} />}
          </span>
          <Avatar src={channel.thumb_url} name={channel.name} size={36} />
          <span className="flex min-w-0 flex-1 flex-col gap-px">
            <span className="truncate text-[14px] font-extrabold">{channel.name}</span>
            <span className="meta text-[12px]">
              {plural(channel.video_count, "video")} · {hint}
            </span>
          </span>
        </button>
        <button
          type="button"
          aria-expanded={showSeries}
          onClick={() => setShowSeries((o) => !o)}
          className={`flex flex-none items-center gap-1 rounded-[8px] px-2 py-1.5 text-[12px] font-bold ${pickedHere > 0 ? "text-accent" : "text-muted-2"} hover:bg-raised/70`}
        >
          {pickedHere > 0 ? `${pickedHere} series` : "Series"}
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" className={showSeries ? "rotate-180" : ""}><path d="M6 9l6 6 6-6" /></svg>
        </button>
      </div>
      {showSeries && (
        <div className="mb-1 ml-[13px] flex flex-col border-l-[1.5px] border-hair pl-4">
          {series.isLoading && <Spinner label="Loading series…" />}
          {!series.isLoading && (series.data ?? []).length === 0 && (
            <p className="meta py-2 text-[12px]">No series archived for this channel.</p>
          )}
          {(series.data ?? []).map((p) => {
            const on = selectedSeries.has(p.id);
            const alsoIn = p.feeds.filter((f) => f.id !== currentFeedId);
            return (
              <button
                key={p.id}
                type="button"
                role="checkbox"
                aria-checked={on}
                disabled={selected}
                onClick={() => onToggleSeries(p.id)}
                title={selected ? "Already in the feed through the whole channel" : undefined}
                className={`flex items-center gap-3 rounded-[10px] px-2 py-2 text-left hover:bg-raised/70 ${selected ? "opacity-45" : ""}`}
              >
                <span className={`flex h-[18px] w-[18px] flex-none items-center justify-center rounded-[6px] text-white ${on ? "bg-accent" : "border-[1.5px] border-hair-2"}`}>
                  {on && <CheckIcon size={10} />}
                </span>
                <span className="flex min-w-0 flex-1 flex-col gap-px">
                  <span className="truncate text-[13px] font-bold">{p.name}</span>
                  <span className="meta text-[12px]">
                    {plural(p.video_count, "video")}
                    {alsoIn.length > 0 && <> · also in {alsoIn.map((f) => f.name).join(", ")}</>}
                  </span>
                </span>
              </button>
            );
          })}
        </div>
      )}
    </div>
  );
}
