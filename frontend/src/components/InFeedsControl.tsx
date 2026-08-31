import { useState } from "react";
import { Link } from "react-router";
import { EVERYTHING_ID } from "@/lib/api";
import { useFeeds } from "@/lib/queries";
import { CheckIcon, Popover } from "@/components/ui";

// "In feeds: Home, DevOps ▾" — the multi-select popover behind the channel's
// and the playlist's feed-membership control. The caller owns the write (PUT
// /channels/:id/feeds or /playlists/:id/feeds) and its invalidations; this
// renders the same picker for both, so the two controls cannot drift.
export function InFeedsControl({ feedIds, onSave }: { feedIds: string[]; onSave: (ids: string[]) => Promise<void> }) {
  const feeds = useFeeds();
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
      await onSave([...next]);
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
