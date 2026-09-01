import { useQuery } from "@tanstack/react-query";
import { api, type PrepareStatus } from "@/lib/api";

// What the background preparation is doing, for the Settings page.
//
// The work itself is invisible by design — its whole point is that a video is
// already scrubbable and already levelled by the time anyone opens it — so the
// only way to know it is happening, or wedged, is to be told. Which is the same
// argument as the player's stats panel, one level up.

/** How often to ask while a pass is in flight, and while it is not. */
const POLL_ACTIVE = 3000;
const POLL_IDLE = 30_000;

export function usePrepareStatus() {
  return useQuery({
    queryKey: ["prepare"],
    queryFn: () => api.prepareStatus(),
    retry: false,
    refetchInterval: (query) => {
      const state = (query.state.data as PrepareStatus | undefined)?.state;
      return state === "running" || state === "paused" ? POLL_ACTIVE : POLL_IDLE;
    },
  });
}

/**
 * One line saying where the job is, and a bar when there is one to draw.
 *
 * `paused` is deliberately its own word rather than a stalled percentage: a
 * job that has stopped because someone is watching something is working
 * exactly as intended, and a progress bar that simply stops moving is the
 * thing people file bugs about.
 */
export function PrepareStatusLine({ status }: { status: PrepareStatus | undefined }) {
  if (!status) return <span className="text-[13px] font-semibold text-muted-2">—</span>;

  if (status.state === "idle") {
    return (
      <span className="text-[13px] font-semibold text-muted-2">
        {status.prepared_at ? `Up to date · last run ${relativeTime(status.prepared_at)}` : "Nothing to do yet"}
      </span>
    );
  }

  const pct = status.total > 0 ? Math.round((status.done / status.total) * 100) : 0;
  return (
    <div className="flex min-w-[220px] flex-col items-end gap-1.5">
      <span className="text-[13px] font-semibold text-ink">
        {status.state === "paused" ? "Paused while you watch" : `Preparing ${status.done + 1} of ${status.total}`}
      </span>
      <div className="h-1 w-full overflow-hidden rounded-sm bg-raised-2">
        <div
          className={`h-full rounded-sm transition-[width] ${status.state === "paused" ? "bg-muted-3" : "bg-accent"}`}
          style={{ width: `${Math.max(pct, 2)}%` }}
        />
      </div>
      {status.video && (
        <span className="max-w-[260px] truncate text-[12px] text-muted-2" title={status.video}>
          {status.video}
        </span>
      )}
    </div>
  );
}

/** "4 minutes ago" from an RFC 3339 stamp; the coarse end is enough here. */
function relativeTime(iso: string): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return "recently";
  const mins = Math.max(0, Math.round((Date.now() - then) / 60_000));
  if (mins < 1) return "just now";
  if (mins < 60) return `${mins} min ago`;
  const hours = Math.round(mins / 60);
  if (hours < 24) return `${hours} h ago`;
  return `${Math.round(hours / 24)} d ago`;
}
