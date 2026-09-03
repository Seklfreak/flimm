import { EVERYTHING_ID, type DeArrowSetting, type FeedSort, type Prefs } from "@/lib/api";
import { useMe, useUpdatePrefs } from "@/lib/queries";
import { useConfig } from "@/lib/config";
import { PageHeader } from "@/components/Layout";
import { PrepareStatusLine, usePrepareStatus } from "@/components/PrepareRow";
import { Segmented, Spinner, Toggle } from "@/components/ui";
import { langName } from "@/player/Player";
import { sponsorCategoryLabel } from "@/player/chapterMath";
import { Link } from "react-router";
import { SUBTITLE_OFF } from "@/player/Player";

// The same preferences the phone and the TV expose, in the same order, because
// they are one account: a viewer who turns sponsor skipping off here expects it
// off on the Apple TV tonight. Everything on this page is a server preference
// (`PATCH /me/prefs`) except where a row says otherwise — quality is per device
// and lives in the player, which is the only place that knows what this browser
// can decode.
// The categories a viewer can have an opinion about, in the order they matter:
// the three that interrupt a video without being part of it, then the ones
// that are sometimes exactly what someone came for. The highlight is not here
// — a point of interest is offered by the player and configures nothing.
const SPONSOR_CATEGORIES = [
  "sponsor",
  "selfpromo",
  "interaction",
  "intro",
  "outro",
  "preview",
  "filler",
  "music_offtopic",
  "exclusive_access",
];

const DEARROW_OPTIONS = [
  { value: "off", label: "Off" },
  { value: "manual", label: "Manual" },
  { value: "all", label: "All" },
];

// "Skip" jumps it, "Ask" offers a button in the player, "Off" leaves it alone.
const SPONSOR_HINTS: Record<string, string> = {
  sponsor: "A paid promotion inside the video.",
  selfpromo: "The creator's own merch, Patreon or channel plug.",
  interaction: "\"Like and subscribe\".",
  intro: "Titles and animation before the video starts.",
  outro: "End cards and credits.",
  preview: "A recap or a preview of what is coming.",
  filler: "A tangent that is not part of the point.",
  music_offtopic: "The non-music parts of a music video.",
  exclusive_access: "A video the creator was given access or a product for.",
};

export default function SettingsPage() {
  const me = useMe();
  const prepare = usePrepareStatus();
  const config = useConfig();
  const prefs = me.data?.prefs;
  const update = useUpdatePrefs();

  const set = (patch: Partial<Prefs>) => update.mutate(patch);

  return (
    <div className="flex flex-col gap-3 pb-16 md:gap-2.5">
      <PageHeader title="Settings" meta={me.data ? config.app_name : undefined} />
      <div className="flex flex-col gap-8 px-5 md:px-10">
        {!prefs ? (
          <Spinner label="Loading settings…" />
        ) : (
          <>
            <Section title="Playback">
              <Row label="Autoplay next video" hint="Play the next video in the list when one ends.">
                <Toggle on={prefs.autoplay} onChange={(v) => set({ autoplay: v })} label="Autoplay next video" />
              </Row>
              <Row label="Playback speed">
                <Segmented
                  value={String(prefs.playback_speed)}
                  onChange={(v) => set({ playback_speed: Number(v) })}
                  options={SPEEDS.map((s) => ({ value: String(s), label: `${s}×` }))}
                />
              </Row>
              <Row
                label="Even out the volume"
                hint="Measures each video once and turns the loud ones down, so you stop reaching for the volume between channels. Nothing is ever turned up, so nothing distorts."
              >
                <Toggle
                  on={prefs.normalize_loudness !== false}
                  onChange={(v) => set({ normalize_loudness: v })}
                  label="Even out the volume"
                />
              </Row>
              <Row
                label="SponsorBlock"
                hint="The master switch. Off, and no segment is skipped, muted or offered — they are still tinted on the timeline."
              >
                <Toggle on={prefs.skip_sponsors} onChange={(v) => set({ skip_sponsors: v })} label="SponsorBlock" />
              </Row>
            </Section>

            {prefs.skip_sponsors && (
              <Section title="SponsorBlock categories">
                {SPONSOR_CATEGORIES.map((category) => (
                  <Row key={category} label={sponsorCategoryLabel(category)} hint={SPONSOR_HINTS[category]}>
                    <Segmented
                      value={prefs.sponsor_actions?.[category] ?? "off"}
                      onChange={(v) => set({ sponsor_actions: { ...prefs.sponsor_actions, [category]: v } })}
                      options={[
                        { value: "skip", label: "Skip" },
                        { value: "ask", label: "Ask" },
                        { value: "off", label: "Off" },
                      ]}
                    />
                  </Row>
                ))}
              </Section>
            )}

            <Section title="DeArrow">
              <Row
                label="Titles"
                hint="Crowd-sourced titles from DeArrow. Manual uses what people submitted and voted on; All also tidies a shouted title nobody has replaced."
              >
                <Segmented
                  value={prefs.dearrow_titles}
                  onChange={(v) => set({ dearrow_titles: v as DeArrowSetting })}
                  options={DEARROW_OPTIONS}
                />
              </Row>
              <Row
                label="Thumbnails"
                hint="Set apart from titles. The frame is cut from your own copy of the video — DeArrow supplies a timestamp, never an image."
              >
                <Segmented
                  value={prefs.dearrow_thumbnails}
                  onChange={(v) => set({ dearrow_thumbnails: v as DeArrowSetting })}
                  options={DEARROW_OPTIONS}
                />
              </Row>
            </Section>

            <Section title="Subtitles">
              <Row label="Language" hint="The track picked by default when a video has one.">
                <select
                  className="rounded-lg border border-hair bg-raised px-3 py-2 text-[13px] font-semibold text-ink"
                  value={prefs.subtitle_lang}
                  onChange={(e) => set({ subtitle_lang: e.target.value })}
                >
                  <option value={SUBTITLE_OFF}>Off</option>
                  {SUBTITLE_LANGS.map((code) => (
                    <option key={code} value={code}>
                      {langName(code)}
                    </option>
                  ))}
                  {/* A language the account picked on another client stays selectable. */}
                  {prefs.subtitle_lang !== SUBTITLE_OFF && !SUBTITLE_LANGS.includes(prefs.subtitle_lang) && (
                    <option value={prefs.subtitle_lang}>{langName(prefs.subtitle_lang)}</option>
                  )}
                </select>
              </Row>
              <Row label="Size">
                <Segmented
                  value={prefs.subtitle_size}
                  onChange={(v) => set({ subtitle_size: v })}
                  options={[
                    { value: "small", label: "Small" },
                    { value: "medium", label: "Medium" },
                    { value: "large", label: "Large" },
                  ]}
                />
              </Row>
            </Section>

            <Section title="“Everything” feed" hint="The built-in feed over every channel. Its options are preferences, not feed settings.">
              <Row label="Sort">
                <Segmented
                  value={prefs.everything_sort}
                  onChange={(v) => set({ everything_sort: v as FeedSort })}
                  options={[
                    { value: "newest", label: "Newest" },
                    { value: "oldest", label: "Oldest" },
                    { value: "shortest", label: "Shortest" },
                    { value: "longest", label: "Longest" },
                  ]}
                />
              </Row>
              <Row label="Hide seen">
                <Toggle
                  on={prefs.everything_hide_seen}
                  onChange={(v) => set({ everything_hide_seen: v })}
                  label="Hide seen"
                />
              </Row>
              <Row label="Include Shorts">
                <Toggle
                  on={prefs.everything_include_shorts}
                  onChange={(v) => set({ everything_include_shorts: v })}
                  label="Include Shorts"
                />
              </Row>
            </Section>

            <Section title="Appearance">
              <Row label="Theme">
                <Segmented
                  value={prefs.theme}
                  onChange={(v) => set({ theme: v })}
                  options={[
                    { value: "system", label: "System" },
                    { value: "light", label: "Light" },
                    { value: "dark", label: "Dark" },
                  ]}
                />
              </Row>
            </Section>

            <Section title="Library">
              <Row label="Feeds" hint="Which channels a feed collects, how it sorts and whether it hides seen videos.">
                <Link
                  to={`/feeds/${EVERYTHING_ID}`}
                  className="rounded-lg border border-hair px-3 py-2 text-[13px] font-bold text-ink no-underline hover:bg-raised"
                >
                  Manage feeds
                </Link>
              </Row>
              <Row
                label="Video quality"
                hint="Chosen in the player and kept on this device only: it depends on what this browser can decode and how big its screen is."
              />
              <Row
                label="Preparing videos"
                hint="Scrub-preview stills and the loudness measurement, derived ahead of time for what is near the top of your feeds, so the first view of a video is not the worst one. It stops while anything is playing. Transcodes are not prepared — they are a thousand times the disk."
              >
                <PrepareStatusLine status={prepare.data} />
              </Row>
            </Section>

            <Section title="Account">
              <Row label="Signed in as">
                <span className="text-[13px] font-semibold text-muted-2">
                  {me.data?.name || me.data?.email || "—"}
                  {me.data?.is_admin ? " · Administrator" : ""}
                </span>
              </Row>
              <Row label="Server version">
                <span className="text-[13px] font-semibold text-muted-2">{config.version || "—"}</span>
              </Row>
              {me.data?.is_admin && (
                <Row
                  label="What the server is doing"
                  hint="Every account's playback right now, what is being transcoded for it, and what recently stalled. Nobody but an administrator can read it."
                >
                  <Link
                    to="/admin"
                    className="rounded-lg border border-hair px-3 py-2 text-[13px] font-bold text-ink no-underline hover:bg-raised"
                  >
                    Live sessions
                  </Link>
                </Row>
              )}
            </Section>
          </>
        )}
      </div>
    </div>
  );
}

const SPEEDS = [0.75, 1, 1.25, 1.5, 2];
// The same shortlist the Apple clients offer (`Shared/PlaybackOptions.swift`).
const SUBTITLE_LANGS = ["en", "de", "es", "fr", "it", "nl", "pt", "pl", "ru", "ja", "ko", "zh"];

function Section({ title, hint, children }: { title: string; hint?: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-1">
      <h2 className="sec pb-1">{title}</h2>
      {hint && <p className="max-w-[620px] pb-2 text-[13px] text-muted-2">{hint}</p>}
      <div className="flex flex-col rounded-xl border border-hair">{children}</div>
    </section>
  );
}

// A label and its control, with the explanation under the label rather than
// beside the control: the row stays readable when the window is narrow, which
// is where a two-column layout would squeeze the text to nothing.
function Row({ label, hint, children }: { label: string; hint?: string; children?: React.ReactNode }) {
  return (
    <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-hair px-4 py-3.5 last:border-b-0">
      <div className="flex min-w-[180px] flex-1 flex-col gap-1">
        <span className="text-[14px] font-bold text-ink">{label}</span>
        {hint && <span className="max-w-[560px] text-[12px] leading-[1.45] text-muted-2">{hint}</span>}
      </div>
      {children}
    </div>
  );
}
