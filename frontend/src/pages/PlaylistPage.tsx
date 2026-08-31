import { useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";
import { useQueryClient } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { invalidateFeedish, usePlaylist, useSetPlaylistMusic, useSetPlaylistPinned, useSetWatched } from "@/lib/queries";
import { ccLabel, fmtDuration, fmtDurationLong, plural } from "@/lib/format";
import { CheckIcon, EmptyState, ErrorState, HeadphonesIcon, PinIcon, Spinner } from "@/components/ui";
import { InFeedsControl } from "@/components/InFeedsControl";
import { VideoRow } from "@/components/VideoRow";
import { watchHref } from "@/components/VideoCard";
import { PlaylistStack } from "./PlaylistsPage";

export default function PlaylistPage() {
  const { id = "" } = useParams();
  const playlist = usePlaylist(id);
  const navigate = useNavigate();
  const qc = useQueryClient();
  const setWatched = useSetWatched();
  const setPinned = useSetPlaylistPinned();
  const setMusic = useSetPlaylistMusic();
  const [unseenOnly, setUnseenOnly] = useState(false);
  const [editing, setEditing] = useState(false);
  // Must sit with the other hooks: everything below the early returns runs
  // conditionally, and a hook there changes hook order between renders.
  const [shuffling, setShuffling] = useState(false);
  const [name, setName] = useState("");

  const p = playlist.data;
  const items = useMemo(() => {
    const all = p?.items ?? [];
    return unseenOnly ? all.filter((i) => !i.video.watched) : all;
  }, [p, unseenOnly]);

  if (playlist.isError) return <ErrorState message={playlist.error.message} retry={() => playlist.refetch()} />;
  if (!p) return <div className="p-10"><Spinner label="Loading playlist…" /></div>;

  const isCustom = p.kind === "custom";
  // Every link this page produces to the player carries the playlist's
  // persisted music intent, so opening a video (or Play/Shuffle) seeds the
  // player already in the right mode.
  const ctx = { playlist: p.id, audio: p.music ? "1" : undefined };
  // A music playlist reports no resume state (songs are replayed, so "seen"
  // is meaningless) — the header/rows always offer a plain Play there.
  const resumeItem = p.music ? undefined : p.items.find((i) => i.video.id === p.resume_video_id);
  // Shuffle hands the player a seed rather than a single random pick: the
  // server derives a stable order from it, so previous/next and autoplay walk
  // the shuffled run instead of falling back to playlist order. A fresh seed
  // each press is what "reshuffle" means. The server owns the ordering, so we
  // ask it where the run starts rather than duplicating the hash here.
  const shuffle = async () => {
    const pool = items.length > 0 ? items : p.items;
    if (pool.length === 0 || shuffling) return;
    const seed = crypto.randomUUID().slice(0, 8);
    const shuffledCtx = { ...ctx, shuffle: seed };
    setShuffling(true);
    try {
      const nav = await api.nav(pool[0].video.id, shuffledCtx);
      const start = nav.first ?? pool[0].video;
      navigate(watchHref(start, shuffledCtx));
    } catch {
      // The order is a nicety; if the lookup fails still start playing.
      navigate(watchHref(pool[0].video, ctx));
    } finally {
      setShuffling(false);
    }
  };
  const move = async (videoId: string, action: "up" | "down" | "top" | "bottom" | "remove") => {
    await api.playlistAction(p.id, videoId, action);
    await qc.invalidateQueries({ queryKey: ["playlists", p.id] });
  };
  const rename = async () => {
    if (name.trim() && name.trim() !== p.name) {
      await api.renamePlaylist(p.id, name.trim());
      void qc.invalidateQueries({ queryKey: ["playlists"] });
    }
  };
  const remove = async () => {
    if (!window.confirm(`Delete playlist “${p.name}”? Videos stay archived.`)) return;
    await api.deletePlaylist(p.id);
    void qc.invalidateQueries({ queryKey: ["playlists"] });
    navigate("/playlists", { replace: true });
  };

  const stats = [
    isCustom ? "Your playlist" : p.channel?.name,
    plural(p.video_count, "video"),
    fmtDurationLong(p.total_duration),
    // A music playlist carries no watch state to summarize (see docs/api.md
    // "Music playlists") — seen_count/in_progress_count come back zeroed.
    !p.music && (p.seen_count > 0 || p.in_progress_count > 0)
      ? `${p.seen_count} seen${p.in_progress_count > 0 ? `, ${p.in_progress_count} in progress` : ""}`
      : undefined,
  ].filter(Boolean);

  return (
    <div className="flex flex-col gap-5 px-5 pb-10 pt-[max(20px,env(safe-area-inset-top))] md:px-10 md:pt-8">
      <div className="flex items-center gap-2 text-[13px] font-semibold text-muted-2">
        <Link to="/playlists" className="text-muted-2 no-underline hover:text-ink">Playlists</Link>
        <span>/</span>
        <span className="truncate text-ink">{p.name}</span>
      </div>
      <div className="flex flex-col gap-4 md:flex-row md:items-center md:gap-6">
        <div className="w-full flex-none md:w-[240px]"><PlaylistStack playlist={p} compact /></div>
        <div className="flex min-w-0 flex-1 flex-col gap-3.5">
          <div className="flex flex-col gap-1">
            {editing ? (
              <form
                className="flex items-center gap-2"
                onSubmit={(e) => {
                  e.preventDefault();
                  void rename().then(() => setEditing(false));
                }}
              >
                <input className="input max-w-sm" value={name} autoFocus onChange={(e) => setName(e.target.value)} />
                <button className="btn pri" type="submit">Save</button>
                <button className="btn" type="button" onClick={() => setEditing(false)}>Cancel</button>
                <button className="btn danger ml-2" type="button" onClick={() => void remove()}>Delete playlist</button>
              </form>
            ) : (
              <span className="h1">{p.name}</span>
            )}
            <span className="meta">{stats.join(" · ")}</span>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            {resumeItem ? (
              <Link to={watchHref(resumeItem.video, ctx)} className="btn pri no-underline">
                <PlayIcon /> Resume · #{resumeItem.position}
              </Link>
            ) : p.items[0] ? (
              <Link to={watchHref(p.items[0].video, ctx)} className="btn pri no-underline">
                <PlayIcon /> Play
              </Link>
            ) : null}
            <button className="btn" onClick={() => void shuffle()} disabled={p.items.length === 0 || shuffling}>
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M4 6h3l10 12h3M20 18l-2 2 2 2M4 18h3l3-4M14 6h3l3 0M20 6l-2-2 2-2" /></svg>
              Shuffle
            </button>
            {/* "Seen"/"unseen" is meaningless for a music playlist — songs are
                replayed and the backend records no watch state for it. */}
            {!p.music && (
              <button className={`btn ${unseenOnly ? "pri" : ""}`} onClick={() => setUnseenOnly((u) => !u)} aria-pressed={unseenOnly}>
                Unseen only
              </button>
            )}
            <button
              className={`btn ${p.music ? "pri" : ""}`}
              aria-pressed={p.music}
              aria-label={p.music ? "Play as video, with watch history" : "Play as music: audio only, no watch history"}
              title={p.music ? "Play as video, with watch history" : "Play as music: audio only, no watch history"}
              onClick={() => setMusic.mutate({ id: p.id, music: !p.music })}
              disabled={setMusic.isPending}
            >
              <HeadphonesIcon size={13} /> Music
            </button>
            <button
              className={`btn ${p.pinned ? "pri" : ""}`}
              aria-pressed={p.pinned}
              aria-label={p.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
              title={p.pinned ? "Unpin from sidebar" : "Pin to sidebar"}
              onClick={() => setPinned.mutate({ id: p.id, pinned: !p.pinned })}
              disabled={setPinned.isPending}
            >
              <PinIcon />
            </button>
            <InFeedsControl
              feedIds={p.feeds.map((f) => f.id)}
              onSave={async (ids) => {
                await api.setPlaylistFeeds(p.id, ids);
                await qc.invalidateQueries({ queryKey: ["playlists", p.id] });
                invalidateFeedish(qc);
              }}
            />
            {isCustom && !editing && (
              <button
                className="btn md:ml-auto"
                onClick={() => {
                  setName(p.name);
                  setEditing(true);
                }}
              >
                Edit
              </button>
            )}
          </div>
          {!p.music && (
            <div className="flex items-center gap-2.5">
              <div className="h-1 flex-1 rounded-sm bg-raised">
                <div className="h-full rounded-sm bg-accent" style={{ width: `${Math.round(p.progress * 100)}%` }} />
              </div>
              <span className="meta text-[12px] !font-bold">{Math.round(p.progress * 100)}%</span>
            </div>
          )}
        </div>
      </div>
      <div className="flex flex-col">
        {items.length === 0 ? (
          <EmptyState title={unseenOnly ? "Everything here is seen" : "This playlist is empty"} />
        ) : (
          items.map((it, idx) => {
            const v = it.video;
            const inProgress = !p.music && !v.watched && v.position > 0;
            return (
              <VideoRow
                key={v.id}
                video={v}
                ctx={ctx}
                dim={v.watched}
                lead={
                  <span className="flex flex-none items-center gap-2">
                    {isCustom && (
                      <span className="hidden flex-col text-muted-3 md:flex">
                        <button aria-label="Move up" className="hover:text-ink disabled:opacity-30" disabled={idx === 0} onClick={() => void move(v.id, "up")}>
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3"><path d="M6 15l6-6 6 6" /></svg>
                        </button>
                        <button aria-label="Move down" className="hover:text-ink disabled:opacity-30" disabled={idx === items.length - 1} onClick={() => void move(v.id, "down")}>
                          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3"><path d="M6 9l6 6 6-6" /></svg>
                        </button>
                      </span>
                    )}
                    <span className="meta w-5 text-right text-[13px] !font-bold">{it.position}</span>
                  </span>
                }
                meta={
                  <>
                    {v.channel.name} · {ccLabel(v.subtitle_langs, v.has_auto_subtitles)}
                    {inProgress && ` · stopped at ${fmtDuration(v.position)}`}
                  </>
                }
                actions={
                  <>
                    {/* A music playlist records no watch state, so every row is
                        simply Play — never Seen or Resume. */}
                    {!p.music && v.watched ? (
                      <button className="btn" onClick={() => setWatched.mutate({ id: v.id, watched: false })} title="Mark unseen">
                        <CheckIcon size={13} /> <span className="hidden sm:inline">Seen</span>
                      </button>
                    ) : (
                      <Link to={watchHref(v, ctx)} className={`btn no-underline ${inProgress ? "pri" : ""}`}>
                        <PlayIcon /> <span className="hidden sm:inline">{inProgress ? "Resume" : "Play"}</span>
                      </Link>
                    )}
                    {isCustom && (
                      <button className="text-muted-3 hover:text-danger" aria-label="Remove from playlist" onClick={() => void move(v.id, "remove")}>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M6 6l12 12M18 6L6 18" /></svg>
                      </button>
                    )}
                  </>
                }
              />
            );
          })
        )}
      </div>
    </div>
  );
}

function PlayIcon() {
  return <svg width="12" height="12" viewBox="0 0 24 24" fill="currentColor"><path d="M7 4l12 8-12 8z" /></svg>;
}
