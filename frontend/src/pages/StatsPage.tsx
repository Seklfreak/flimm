import { useState } from "react";
import { Link } from "react-router";
import type { StatsRange, WatchStats } from "@/lib/api";
import { useStats } from "@/lib/queries";
import { compactCount, fmtDurationLong } from "@/lib/format";
import { PageHeader } from "@/components/Layout";
import { EmptyState, ErrorState, Segmented, Spinner } from "@/components/ui";

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

export default function StatsPage() {
  const [range, setRange] = useState<StatsRange>("all");
  const stats = useStats(range);

  return (
    <div className="flex flex-col gap-3 pb-10 md:gap-2.5">
      <PageHeader
        title="Stats"
        meta={stats.data ? coverage(stats.data) : undefined}
        actions={
          <Segmented
            value={range}
            onChange={setRange}
            options={[
              { value: "all", label: "All time" },
              { value: "year", label: "This year" },
              { value: "month", label: "This month" },
            ]}
          />
        }
      />
      <div className="flex flex-col gap-4 px-5 md:px-10">
        {stats.isLoading && <Spinner />}
        {stats.isError && <ErrorState message="Couldn't load your stats." />}
        {stats.data && stats.data.started === 0 && (
          <EmptyState
            title="Nothing watched yet"
            hint="Play something and this fills in — what you watched, whose videos, and when."
          />
        )}
        {stats.data && stats.data.started > 0 && <Report stats={stats.data} />}
      </div>
    </div>
  );
}

function Report({ stats }: { stats: WatchStats }) {
  const finishRate = stats.started > 0 ? Math.round((stats.finished / stats.started) * 100) : 0;
  return (
    <>
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Headline label="Watched" value={fmtDurationLong(stats.seconds)} />
        <Headline label="Videos started" value={compactCount(stats.started)} />
        <Headline label="Finished" value={compactCount(stats.finished)} />
        <Headline label="Finish rate" value={`${finishRate}%`} />
      </div>

      {stats.top_channels.length > 0 && (
        <Card title="Whose videos">
          <div className="flex flex-col gap-2.5">
            {stats.top_channels.map((c) => (
              <Bar
                key={c.id}
                max={stats.top_channels[0].seconds}
                value={c.seconds}
                label={
                  <Link to={`/channels/${c.id}`} className="font-bold text-ink no-underline hover:text-accent">
                    {c.name}
                  </Link>
                }
                trailing={`${fmtDurationLong(c.seconds)} · ${c.videos}`}
              />
            ))}
          </div>
        </Card>
      )}

      <div className="grid gap-4 md:grid-cols-2">
        <Card title="When you start watching" note={hourNote(stats)}>
          <Columns
            values={stats.by_hour}
            labelFor={(i) => (i % 6 === 0 ? `${i}:00` : "")}
            titleFor={(i, v) => `${i}:00 — ${v} ${v === 1 ? "video" : "videos"}`}
          />
        </Card>
        <Card title="Which days">
          <Columns
            values={stats.by_weekday}
            labelFor={(i) => WEEKDAYS[i]}
            titleFor={(i, v) => `${WEEKDAYS[i]} — ${v} ${v === 1 ? "video" : "videos"}`}
          />
        </Card>
      </div>

      {stats.by_month.length > 0 && (
        <Card title="Month by month">
          <Columns
            values={stats.by_month.map((m) => m.videos)}
            labelFor={(i) => monthLabel(stats.by_month[i].month)}
            titleFor={(i, v) => `${stats.by_month[i].month} — ${v} ${v === 1 ? "video" : "videos"}`}
          />
        </Card>
      )}

      {/* Said plainly, because an invented number here would look just like a
          real one. See docs/api.md, "Watch stats". */}
      <p className="meta px-1">
        “Watched” is the furthest point reached in each video, added up: a finished video counts in full, an abandoned
        one counts where it stopped, and watching something twice counts once. Times of day are when a video was first
        started, in your own timezone ({stats.zone}).
      </p>
    </>
  );
}

function Headline({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[14px] bg-raised-2 p-4">
      <div className="text-[22px] font-extrabold leading-tight tracking-[-0.02em]">{value}</div>
      <div className="meta mt-0.5">{label}</div>
    </div>
  );
}

function Card({ title, note, children }: { title: string; note?: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-3 rounded-[14px] bg-raised-2 p-4">
      <div className="flex items-baseline justify-between gap-3">
        <h2 className="text-[14px] font-extrabold">{title}</h2>
        {note && <span className="meta">{note}</span>}
      </div>
      {children}
    </div>
  );
}

function Bar({
  value,
  max,
  label,
  trailing,
}: {
  value: number;
  max: number;
  label: React.ReactNode;
  trailing: string;
}) {
  const width = max > 0 ? Math.max(2, Math.round((value / max) * 100)) : 0;
  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-baseline justify-between gap-3 text-[13px]">
        <span className="truncate">{label}</span>
        <span className="meta flex-none">{trailing}</span>
      </div>
      <div className="h-2 overflow-hidden rounded-full bg-raised">
        <div className="h-full rounded-full bg-accent" style={{ width: `${width}%` }} />
      </div>
    </div>
  );
}

/**
 * A row of columns. Heights are relative to the busiest one, with a visible
 * stub for a real-but-small value — a column of zero height and a column of
 * "one video" must not look the same.
 */
function Columns({
  values,
  labelFor,
  titleFor,
}: {
  values: number[];
  labelFor: (i: number) => string;
  titleFor: (i: number, v: number) => string;
}) {
  const max = Math.max(...values, 1);
  return (
    // `items-stretch` (the default) matters: with `items-end` the columns
    // shrink to their labels, and a bar sized as a percentage of a zero-height
    // parent draws nothing at all.
    <div className="flex items-stretch justify-start gap-[3px]" style={{ height: 96 }}>
      {values.map((v, i) => (
        // Capped so a single month (or a quiet week) is a bar rather than a
        // wall: one value spread across the whole card reads as a fill, not a
        // measurement.
        <div key={i} className="flex flex-1 flex-col items-center gap-1" style={{ maxWidth: 56 }} title={titleFor(i, v)}>
          <div className="flex w-full flex-1 items-end">
            <div
              className={`w-full rounded-[3px] ${v > 0 ? "bg-accent" : "bg-raised"}`}
              style={{ height: v > 0 ? `${Math.max(6, (v / max) * 100)}%` : 2 }}
            />
          </div>
          <span className="meta text-[10px] leading-none">{labelFor(i)}</span>
        </div>
      ))}
    </div>
  );
}

function coverage(stats: WatchStats): string {
  if (stats.range === "year") return "this year";
  if (stats.range === "month") return "this month";
  return stats.since ? `since ${new Date(stats.since).toLocaleDateString(undefined, { month: "long", year: "numeric" })}` : "";
}

function hourNote(stats: WatchStats): string {
  const max = Math.max(...stats.by_hour);
  if (max === 0) return "";
  const hour = stats.by_hour.indexOf(max);
  return `busiest at ${hour}:00`;
}

/** `2026-08` → `Aug`, and `Jan` carries its year so a 12-month row reads. */
function monthLabel(month: string): string {
  const [year, m] = month.split("-");
  const date = new Date(Number(year), Number(m) - 1, 1);
  const short = date.toLocaleDateString(undefined, { month: "short" });
  return m === "01" ? `${short} ${year.slice(2)}` : short;
}
