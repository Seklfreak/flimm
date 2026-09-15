import { Link } from "react-router";
import type { DownloadsResponse, LiveJob, LiveResponse, LiveSession, LiveStall, QueuedItem } from "@/lib/api";
import { useDownloads, useLiveSessions, useMe } from "@/lib/queries";
import { fmtDuration } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { EmptyState, ErrorState, ProgressBar, Spinner } from "@/components/ui";

/**
 * What the server is doing right now.
 *
 * Every other screen in Flimm is per-viewer and after the fact — history says
 * what was watched, stats say how much of it. This is the present tense, and
 * it is the only screen that crosses the account boundary: whoever runs the
 * archive is the person who has to answer for what the box is doing, and
 * cannot from a view that shows only their own screens.
 *
 * The three questions it exists to answer, in the order they get asked: is
 * anything playing, is the machine transcoding for it, and is anybody watching
 * a spinner. Everything on it is either something the server observed for
 * itself (the delivery path, the bytes, the encoder's position) or something a
 * player published about itself, and the two are kept visibly apart — a
 * reading that came from a television is labelled as the television's.
 *
 * It is deliberately web only. A maintenance view is read with a terminal
 * open next to it, and that is not a thing anybody does at two metres from a
 * television or one-handed on a phone. See docs/apple-apps.md.
 */
export default function AdminPage() {
  const me = useMe();
  const live = useLiveSessions();
  const downloads = useDownloads();

  if (!me.data?.is_admin) {
    return (
      <div className="flex flex-col gap-3 pb-10">
        <PageHeader title="Server" />
        <div className="px-5 md:px-10">
          <EmptyState
            title="Administrators only"
            hint="This is what every account on the server is playing right now, so it is not yours to read unless you run it."
          />
        </div>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-3 pb-10 md:gap-2.5">
      <PageHeader title="Server" meta={live.data ? headline(live.data, downloads.data) : undefined} />
      <div className="flex flex-col gap-7 px-5 pt-1 md:px-10 md:pt-2">
        {live.isLoading && <Spinner />}
        {live.isError && <ErrorState message="Couldn't load what the server is doing." retry={() => void live.refetch()} />}
        {live.data && <Report live={live.data} />}
        {downloads.isError ? (
          <ErrorState message="Couldn't load the download queue." retry={() => void downloads.refetch()} />
        ) : (
          downloads.data && <Downloads downloads={downloads.data} />
        )}
      </div>
    </div>
  );
}

/** The one line under the title: the counts worth knowing before reading. */
function headline(live: LiveResponse, downloads?: DownloadsResponse): string {
  const parts = [`${live.sessions.length} ${live.sessions.length === 1 ? "session" : "sessions"}`];
  if (live.jobs.length > 0) parts.push(`${live.jobs.length} transcoding`);
  if (live.stalls.length > 0) parts.push(`${live.stalls.length} recent ${live.stalls.length === 1 ? "stall" : "stalls"}`);
  if (downloads) {
    if (downloads.active.length > 0) parts.push(`${downloads.active.length} downloading`);
    // A blocked queue is the one number on this page that means somebody has
    // to do something, so it is said before anything is scrolled to.
    if (downloads.counts.blocked > 0) parts.push(`${downloads.counts.blocked} blocked`);
  }
  return parts.join(" · ");
}

function Report({ live }: { live: LiveResponse }) {
  const now = new Date(live.now).getTime();
  // A transcode with no session attached to it is the thing this page exists
  // to make visible: nobody is watching, and the box is encoding anyway.
  const watched = new Set(
    live.sessions.filter((s) => s.delivery.job).map((s) => jobKey(s.delivery.job as LiveJob)),
  );
  const orphaned = live.jobs.filter((j) => !watched.has(jobKey(j)));

  return (
    <>
      <section className="flex flex-col gap-2">
        <h2 className="sec">Playing now</h2>
        {live.sessions.length === 0 ? (
          <EmptyState title="Nothing is playing" hint="Sessions appear here within a heartbeat of anyone pressing play, on any client." />
        ) : (
          <div className="flex flex-col gap-2.5">
            {live.sessions.map((s) => (
              <SessionCard key={`${s.user_id}-${s.video_id}`} session={s} now={now} />
            ))}
          </div>
        )}
      </section>

      {orphaned.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="sec">Transcoding for nobody</h2>
          <p className="max-w-[620px] text-[13px] text-muted-2">
            A rendition still being derived with no session attached to it — usually a viewer who closed the tab. It
            finishes anyway, and the next person to ask for that rung gets it for free.
          </p>
          <div className="flex flex-col gap-2.5">
            {orphaned.map((job) => (
              <div key={jobKey(job)} className="rounded-2xl border border-hair bg-surface px-4 py-3">
                <Link to={`/watch/${job.video_id}`} className="text-[13px] font-bold text-ink no-underline hover:text-accent">
                  {job.video_id}
                </Link>
                <JobLine job={job} />
              </div>
            ))}
          </div>
        </section>
      )}

      {live.stalls.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="sec">Recent stalls</h2>
          <p className="max-w-[620px] text-[13px] text-muted-2">
            A viewer watched a spinner and said so. The reason is the server's own: whether the segment being waited for
            existed yet decides which half of the system to go and look at.
          </p>
          <div className="overflow-x-auto rounded-2xl border border-hair bg-surface">
            <table className="w-full min-w-[560px] border-collapse text-[12.5px]">
              <thead>
                <tr className="text-left text-[11px] font-extrabold uppercase tracking-[0.08em] text-muted-3">
                  <th className="px-4 py-2.5 font-extrabold">When</th>
                  <th className="px-4 py-2.5 font-extrabold">Video</th>
                  <th className="px-4 py-2.5 font-extrabold">At</th>
                  <th className="px-4 py-2.5 font-extrabold">For</th>
                  <th className="px-4 py-2.5 font-extrabold">Why</th>
                  <th className="px-4 py-2.5 font-extrabold">Client</th>
                </tr>
              </thead>
              <tbody>
                {[...live.stalls].reverse().map((stall, i) => (
                  <StallRow key={`${stall.at}-${i}`} stall={stall} now={now} />
                ))}
              </tbody>
            </table>
          </div>
        </section>
      )}
    </>
  );
}

/**
 * What the archive is fetching, and what it has quietly given up on.
 *
 * TubeArchivist answers two different questions and neither one alone is the
 * truth. The progress message says something is moving right now; it expires
 * seconds after the work does, so silence here means idle, not broken. The
 * queue says how much is waiting — and hides the failure that matters: the
 * downloader only picks items with no stored error message, so anything that
 * has ever failed is invisible to it forever, with nothing to retry it. A
 * queue of a thousand that can only move on twelve of them looks, in TA's own
 * total, exactly like a healthy backlog.
 *
 * Which is why `blocked` is its own number and its own colour. Flimm does not
 * offer to fix it: clearing the message is a write to instance-wide state, and
 * this view is for finding out, not for operating the archive.
 */
function Downloads({ downloads }: { downloads: DownloadsResponse }) {
  const { active, counts, pending } = downloads;
  return (
    <section className="flex flex-col gap-2">
      <h2 className="sec">Downloading</h2>

      {active.length === 0 ? (
        <p className="max-w-[620px] text-[13px] text-muted-2">
          Nothing is downloading right now. TubeArchivist reports progress only while it is working, so an empty line
          here means idle rather than broken — the queue below is what says whether it has anything left to do.
        </p>
      ) : (
        <div className="flex flex-col gap-2.5">
          {active.map((a, i) => (
            <article key={`${a.title}-${i}`} className="flex flex-col gap-1.5 rounded-2xl border border-hair bg-surface px-4 py-3">
              <span className="truncate text-[14px] font-bold text-ink">{a.title}</span>
              {a.progress !== null && (
                <div className="flex items-center gap-3">
                  <ProgressBar value={a.progress} className="max-w-[320px] flex-1" />
                  <span className="text-[12px] font-semibold text-muted-2">{Math.round(a.progress * 100)}%</span>
                </div>
              )}
              {/* TubeArchivist's own line. The rate and the ETA live nowhere
                  else, so it is shown as it stands rather than taken apart. */}
              {a.detail && (
                <p className={`text-[12.5px] font-semibold ${a.level === "error" ? "text-danger" : "text-muted-2"}`}>{a.detail}</p>
              )}
            </article>
          ))}
        </div>
      )}

      <div className="flex flex-wrap items-center gap-2 pt-1">
        {counts.ready + counts.blocked + counts.ignored === 0 ? (
          <span className="chip">queue empty</span>
        ) : (
          <>
            <span className="chip">{counts.ready} ready</span>
            {counts.blocked > 0 && <span className="chip text-danger">{counts.blocked} blocked</span>}
            {counts.ignored > 0 && <span className="chip">{counts.ignored} ignored</span>}
          </>
        )}
      </div>

      {counts.blocked > 0 && (
        <p className="max-w-[620px] text-[13px] text-muted-2">
          Blocked items recorded an error once and now carry it forever: TubeArchivist's downloader skips anything with
          a stored message, and nothing clears it on its own. They will never be fetched until someone gives them
          priority again in TubeArchivist, however long the queue sits there.
        </p>
      )}

      {pending.length > 0 && (
        <div className="flex flex-col gap-1.5 pt-1">
          {pending.map((item) => (
            <QueueRow key={item.id} item={item} />
          ))}
        </div>
      )}
    </section>
  );
}

function QueueRow({ item }: { item: QueuedItem }) {
  return (
    <div className="flex flex-col gap-0.5 rounded-2xl border border-hair bg-surface px-4 py-2.5">
      <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
        <span className="min-w-0 flex-1 truncate text-[13px] font-bold text-ink">{item.title || item.id}</span>
        {item.auto_start && <span className="chip">priority</span>}
        {item.seconds > 0 && <span className="text-[12px] font-semibold text-muted-2">{fmtDuration(item.seconds)}</span>}
      </div>
      <Link to={`/channels/${item.channel_id}`} className="truncate text-[12.5px] font-semibold text-muted-2 no-underline hover:text-accent">
        {item.channel_name}
      </Link>
      {item.error && <p className="text-[12.5px] font-semibold text-danger">{item.error}</p>}
    </div>
  );
}

function SessionCard({ session, now }: { session: LiveSession; now: number }) {
  const progress = session.duration > 0 ? session.position / session.duration : 0;
  return (
    <article className="flex flex-col gap-1.5 rounded-2xl border border-hair bg-surface px-4 py-3">
      <div className="flex flex-wrap items-baseline justify-between gap-x-4 gap-y-1">
        <div className="flex min-w-0 flex-wrap items-baseline gap-2">
          <span className="truncate text-[13px] font-bold text-ink">{session.user || session.user_id}</span>
          <span className="chip">{clientLabel(session)}</span>
          {session.paused && <span className="chip">paused</span>}
          {session.streaming && <span className="chip">streaming</span>}
        </div>
        <span className="text-[12px] font-semibold text-muted-2">{ago(session.updated_at, now)}</span>
      </div>

      <Link to={`/watch/${session.video_id}`} className="truncate text-[14px] font-bold text-ink no-underline hover:text-accent">
        {session.title || session.video_id}
      </Link>
      {session.channel_name && <span className="text-[12.5px] font-semibold text-muted-2">{session.channel_name}</span>}

      <div className="flex items-center gap-3">
        <ProgressBar value={progress} className="max-w-[320px] flex-1" />
        <span className="text-[12px] font-semibold text-muted-2">
          {fmtDuration(session.position)}
          {session.duration > 0 && ` / ${fmtDuration(session.duration)}`}
        </span>
      </div>

      <p className="text-[12.5px] font-semibold text-muted-2">{describeDelivery(session)}</p>
      {session.delivery.job && <JobLine job={session.delivery.job} />}
      {session.stalls > 0 && (
        <p className="text-[12.5px] font-semibold text-danger">
          {session.stalls} {session.stalls === 1 ? "stall" : "stalls"}
          {session.last_stall && ` · last one ${stallReason(session.last_stall)}`}
        </p>
      )}
      {session.stats && <PublishedStats stats={session.stats} />}
    </article>
  );
}

/**
 * The transcode behind a rendition.
 *
 * `progress` is how much of the rendition exists, which is *not* where
 * playback is — the two are read together on purpose: a viewer ahead of the
 * encoder is the one cause of a stall the server can actually fix.
 */
function JobLine({ job }: { job: LiveJob }) {
  const queued = job.encoder_segment < 0;
  return (
    <p className="text-[12.5px] font-semibold text-muted-2">
      {queued
        ? `${job.height}p · queued for the transcode slot`
        : `${job.height}p · encoding segment ${job.encoder_segment} of ${job.segments} · ${Math.round(job.progress * 100)}% derived`}
    </p>
  );
}

/** What a player said about itself, kept visibly apart from what the server saw. */
function PublishedStats({ stats }: { stats: NonNullable<LiveSession["stats"]> }) {
  const parts: string[] = [];
  if (stats.delivery.reason) parts.push(stats.delivery.reason);
  if (stats.player.status) parts.push(stats.player.status);
  if (stats.player.buffer_ahead !== undefined) parts.push(`${stats.player.buffer_ahead.toFixed(1)}s buffered`);
  if (stats.player.observed_bitrate) parts.push(`${(stats.player.observed_bitrate / 1_000_000).toFixed(1)} Mbps`);
  if (stats.player.dropped_frames) parts.push(`${stats.player.dropped_frames} dropped`);
  if (stats.player.picture_width > 0) parts.push(`${stats.player.picture_width}×${stats.player.picture_height}`);
  if (parts.length === 0) return null;
  return <p className="text-[12.5px] font-semibold text-muted-3">Reported by the player: {parts.join(" · ")}</p>;
}

function StallRow({ stall, now }: { stall: LiveStall; now: number }) {
  return (
    <tr className="border-t border-hair align-top">
      <td className="px-4 py-2 font-semibold text-muted-2 whitespace-nowrap">{ago(stall.at, now)}</td>
      <td className="px-4 py-2 font-semibold">
        <Link to={`/watch/${stall.video_id}`} className="text-ink no-underline hover:text-accent">
          {stall.video_id}
        </Link>
      </td>
      <td className="px-4 py-2 font-semibold text-muted-2 whitespace-nowrap">{fmtDuration(stall.position)}</td>
      <td className="px-4 py-2 font-semibold text-muted-2 whitespace-nowrap">{stall.seconds.toFixed(1)}s</td>
      <td className="px-4 py-2 font-semibold text-muted-2">{stallReason(stall.reason)}</td>
      <td className="px-4 py-2 font-semibold text-muted-2 whitespace-nowrap">
        {stall.client || "—"}
        {stall.height > 0 && ` · ${stall.height}p`}
      </td>
    </tr>
  );
}

function jobKey(job: LiveJob): string {
  return `${job.video_id}-${job.height}`;
}

/**
 * How the video is reaching the screen, in the words the playback stats panel
 * uses — the point of the page is that a video quietly costing the server an
 * encode must not read like one that costs it nothing.
 */
function describeDelivery(session: LiveSession): string {
  const { kind, height } = session.delivery;
  const rung = height ? ` ${height}p` : "";
  const sent = session.bytes > 0 ? ` · ${fmtBytes(session.bytes)} sent` : "";
  switch (kind) {
    case "direct":
      return `Direct play${rung} — the archived file itself${sent}`;
    case "rendition":
      return `Transcoded${rung} — a compatible rendition${sent}`;
    case "audio":
      return `Audio only${sent}`;
    default:
      // A heartbeat with no media request behind it: either the player has
      // buffered everything it needs, or it started before this server did.
      return "Nothing requested yet — buffered, or playing from before the server started";
  }
}

/** The server's four attributions, said as a person would say them. */
function stallReason(reason: string): string {
  switch (reason) {
    case "encoder_behind":
      return "the encoder was behind the viewer";
    case "delivery":
      return "the bytes existed — network, buffer or decoder";
    case "source":
      return "playing the archive directly";
    default:
      return "unknown — the rendition was evicted since";
  }
}

function clientLabel(session: LiveSession): string {
  const name = session.device ? `${session.device} · ` : "";
  switch (session.client) {
    case "tvos":
      return `${name}Apple TV`;
    case "ios":
      return `${name}iPhone`;
    case "ipados":
      return `${name}iPad`;
    case "web":
      return `${name}web`;
    case "apple":
      return `${name}Apple client`;
    default:
      return name ? name.slice(0, -3) : "unknown client";
  }
}

/** Bytes as a person reads them; the units stop at GB because a session that
 *  sent a terabyte is a different bug. */
function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 ** 2) return `${(n / 1024).toFixed(0)} kB`;
  if (n < 1024 ** 3) return `${(n / 1024 ** 2).toFixed(1)} MB`;
  return `${(n / 1024 ** 3).toFixed(2)} GB`;
}

/**
 * How long ago, against the *server's* clock.
 *
 * The response carries the server's `now` for exactly this: the difference
 * between a browser's clock and a server's is unbounded, and a page that aged
 * these against the browser's would quietly report a live session as minutes
 * stale on any machine whose time has drifted.
 */
function ago(iso: string, now: number): string {
  const seconds = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (seconds < 5) return "just now";
  if (seconds < 60) return `${seconds}s ago`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes} min ago`;
  return `${Math.round(minutes / 60)} h ago`;
}
