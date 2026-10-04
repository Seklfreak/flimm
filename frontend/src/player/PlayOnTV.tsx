import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type PlayContext, type RemoteReceiver } from "@/lib/api";
import { keys } from "@/lib/queries";
import { Popover } from "@/components/ui";

// How often the list of televisions is refreshed while a video is open. A TV
// registers while its app is in front and lapses 45 s after it leaves, so this
// is about as stale as the menu can get.
const RECEIVER_REFRESH_MS = 15_000;

// "Play on Living Room": hands the video to an Apple TV with Flimm open. Drawn
// only when there is one. The browser pauses rather than closes — on a desktop
// the page is still the place to read the description and the comments — and
// the television takes it from where it was, in the same feed or playlist.
export function PlayOnTV({
  videoId,
  ctx,
  currentTime,
  onCast,
}: {
  videoId: string;
  ctx: PlayContext;
  currentTime: () => number;
  onCast: () => void;
}) {
  const [anchor, setAnchor] = useState<HTMLButtonElement | null>(null);
  const [open, setOpen] = useState(false);
  const [status, setStatus] = useState<{ device: string; ok: boolean } | null>(null);
  const [busy, setBusy] = useState(false);
  const listing = useQuery({
    queryKey: keys.remote,
    queryFn: api.remoteSessions,
    refetchInterval: RECEIVER_REFRESH_MS,
    // A server without casting, or one that is down: nothing to offer, which
    // is not worth an error on the watch page.
    retry: false,
  });
  const receivers = listing.data?.receivers ?? [];
  if (receivers.length === 0) return null;

  const name = (r: RemoteReceiver) => r.device || "Apple TV";
  const playOn = async (r: RemoteReceiver) => {
    setOpen(false);
    setBusy(true);
    try {
      await api.playOn(r.id, videoId, currentTime(), ctx);
      onCast();
      setStatus({ device: name(r), ok: true });
    } catch {
      setStatus({ device: name(r), ok: false });
    } finally {
      setBusy(false);
    }
  };
  // One television is the common case, and then the button can say where it
  // sends the video without a menu in between.
  const only = receivers.length === 1 ? receivers[0] : undefined;
  const label = status ? (status.ok ? `Playing on ${status.device}` : `Couldn't reach ${status.device}`) : only ? `Play on ${name(only)}` : "Play on TV";

  return (
    <>
      <button
        ref={setAnchor}
        className="btn"
        disabled={busy}
        onClick={() => (only ? void playOn(only) : setOpen((o) => !o))}
        aria-haspopup={only ? undefined : "menu"}
        aria-expanded={only ? undefined : open}
      >
        <TVIcon />
        {label}
      </button>
      {open && (
        <Popover anchor={anchor} onClose={() => setOpen(false)} width={220}>
          <div className="pop" role="menu">
            <span className="sec px-2.5 pb-1 pt-1.5 !text-muted-3">Play on</span>
            {receivers.map((r) => (
              <button key={r.id} role="menuitem" className="pop-item" onClick={() => void playOn(r)}>
                <span className="truncate">{name(r)}</span>
              </button>
            ))}
          </div>
        </Popover>
      )}
    </>
  );
}

function TVIcon({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <rect x="2" y="4" width="20" height="13" rx="2" />
      <path d="M8 21h8M12 17v4" />
    </svg>
  );
}
