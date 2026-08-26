import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { api, type VideoPlaylistRef } from "@/lib/api";
import { usePlaylists } from "@/lib/queries";
import { CheckIcon, Popover } from "@/components/ui";

// "Add to playlist" popover: custom playlists with membership check marks,
// plus an inline "New playlist" form.
export function AddToPlaylist({ videoId, memberOf }: { videoId: string; memberOf: VideoPlaylistRef[] }) {
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  const [newName, setNewName] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const lists = usePlaylists("custom");
  const qc = useQueryClient();
  const custom = lists.data?.pages.flatMap((p) => p.items) ?? [];
  const member = new Set(memberOf.map((p) => p.id));

  const refresh = () => {
    void qc.invalidateQueries({ queryKey: ["playlists"] });
    void qc.invalidateQueries({ queryKey: ["videos", videoId] });
  };
  const toggle = async (id: string) => {
    setBusy(id);
    try {
      await api.playlistAction(id, videoId, member.has(id) ? "remove" : "add");
      refresh();
    } finally {
      setBusy(null);
    }
  };
  const create = async () => {
    if (!newName.trim()) return;
    setBusy("new");
    try {
      const p = await api.createPlaylist(newName.trim());
      await api.playlistAction(p.id, videoId, "add");
      setNewName("");
      refresh();
    } finally {
      setBusy(null);
    }
  };

  return (
    <>
      <button ref={setAnchor} className="btn" onClick={() => setOpen((o) => !o)} aria-haspopup="dialog" aria-expanded={open}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M12 5v14M5 12h14" /></svg>
        Add to playlist
      </button>
      {open && (
        <Popover anchor={anchor} onClose={() => setOpen(false)} width={280}>
          <div className="pop">
            <span className="sec px-2.5 pb-1 pt-1.5 !text-muted-3">Playlists</span>
            {lists.isLoading && <span className="px-2.5 py-2 text-muted-3">Loading…</span>}
            {custom.length === 0 && !lists.isLoading && <span className="px-2.5 py-2 text-muted-3">No playlists yet</span>}
            {custom.map((p) => (
              <button key={p.id} className={`pop-item ${member.has(p.id) ? "on" : ""}`} onClick={() => void toggle(p.id)} disabled={busy !== null}>
                <span className="truncate">{p.name}</span>
                {member.has(p.id) ? <CheckIcon size={14} /> : <span className="text-[11px] text-muted-3">{p.video_count}</span>}
              </button>
            ))}
            <form
              className="mt-1 flex items-center gap-1.5 border-t border-white/10 pt-2"
              onSubmit={(e) => {
                e.preventDefault();
                void create();
              }}
            >
              <input
                value={newName}
                onChange={(e) => setNewName(e.target.value)}
                placeholder="New playlist"
                className="min-w-0 flex-1 rounded-lg bg-white/10 px-2.5 py-1.5 text-white outline-none placeholder:text-white/40"
              />
              <button type="submit" className="rounded-lg bg-accent px-2.5 py-1.5 text-[12px] font-bold text-white disabled:opacity-50" disabled={!newName.trim() || busy !== null}>
                Create
              </button>
            </form>
          </div>
        </Popover>
      )}
    </>
  );
}
