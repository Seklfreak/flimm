// Duration in seconds → "9:21" / "1:02:03".
export function fmtDuration(s: number): string {
  const t = Math.max(0, Math.floor(s));
  const h = Math.floor(t / 3600);
  const m = Math.floor((t % 3600) / 60);
  const sec = t % 60;
  const mm = h > 0 ? String(m).padStart(2, "0") : String(m);
  return `${h > 0 ? h + ":" : ""}${mm}:${String(sec).padStart(2, "0")}`;
}

// Total duration → "4 h 12 min" / "48 min".
export function fmtDurationLong(s: number): string {
  const h = Math.floor(s / 3600);
  const m = Math.round((s % 3600) / 60);
  if (h === 0) return `${m} min`;
  return m === 0 ? `${h} h` : `${h} h ${m} min`;
}

const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];

function startOfDay(d: Date): Date {
  return new Date(d.getFullYear(), d.getMonth(), d.getDate());
}

function dayDiff(iso: string, now: Date): number {
  const d = startOfDay(new Date(iso)).getTime();
  const n = startOfDay(now).getTime();
  return Math.round((n - d) / 86_400_000);
}

// "today" / "yesterday" / "3 days ago" / "Sunday" / "last week" / "2 weeks ago" / "Mar 3".
export function relativeDay(iso: string, now: Date = new Date()): string {
  const days = dayDiff(iso, now);
  if (days <= 0) return "today";
  if (days === 1) return "yesterday";
  if (days < 7) return `${days} days ago`;
  if (days < 14) return "last week";
  if (days < 30) return `${Math.floor(days / 7)} weeks ago`;
  const d = new Date(iso);
  const opts: Intl.DateTimeFormatOptions =
    d.getFullYear() === now.getFullYear()
      ? { month: "short", day: "numeric" }
      : { month: "short", day: "numeric", year: "numeric" };
  return d.toLocaleDateString(undefined, opts);
}

// "seen Sunday" flavour: today / yesterday / weekday within a week / last week / date.
export function seenLabel(iso: string | null, now: Date = new Date()): string {
  if (!iso) return "seen";
  const days = dayDiff(iso, now);
  if (days <= 0) return "seen today";
  if (days === 1) return "seen yesterday";
  if (days < 7) return `seen ${WEEKDAYS[new Date(iso).getDay()]}`;
  return `seen ${relativeDay(iso, now)}`;
}

// History group heading: Today / Yesterday / weekday / "Mon, Aug 18".
export function dayHeading(iso: string, now: Date = new Date()): string {
  const days = dayDiff(iso, now);
  if (days <= 0) return "Today";
  if (days === 1) return "Yesterday";
  if (days < 7) return WEEKDAYS[new Date(iso).getDay()];
  return new Date(iso).toLocaleDateString(undefined, {
    weekday: "short",
    month: "short",
    day: "numeric",
  });
}

export function dayKey(iso: string): string {
  const d = new Date(iso);
  return `${d.getFullYear()}-${d.getMonth()}-${d.getDate()}`;
}

export function fmtClock(iso: string): string {
  return new Date(iso).toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
}

// "CC EN" / "CC EN, DE" / "no subtitles"; auto tracks count as "CC EN (auto)".
export function ccLabel(langs: string[], hasAuto: boolean): string {
  if (langs.length > 0) return `CC ${langs.map((l) => l.toUpperCase()).join(", ")}`;
  if (hasAuto) return "CC auto";
  return "no subtitles";
}

export function plural(n: number, one: string, many = one + "s"): string {
  return `${n} ${n === 1 ? one : many}`;
}
