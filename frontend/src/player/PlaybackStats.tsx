import { useEffect, useState } from "react";
import type { HLSState, HLSVariant, Loudness, Video } from "@/lib/api";
import { fmtDuration } from "@/lib/format";
import type { DeviceCapabilities, PlaybackReason } from "./codecGate";
import type { PreviewStatus } from "./preview";
import {
  bufferedAhead,
  describeDecoders,
  describeDelivery,
  describeDroppedFrames,
  describeLoudness,
  describePreview,
  describeRendition,
  describeStream,
  elementState,
} from "./statsText";

/**
 * The playback stats panel: what this player is actually doing.
 *
 * Everything Flimm derives is invisible by design — a transcode a viewer never
 * asked for, a sheet of stills queued behind it, a measurement that quietly
 * turns the volume down — and every one of them fails in a way that looks
 * exactly like nothing happening. This is where they say so.
 *
 * It reads; it never decides. Every value comes from what the player itself
 * runs on: the gate's own `reason`, the cache's own job states, the element's
 * own counters. A panel that worked anything out for itself could disagree
 * with the picture, which would make it worse than no panel.
 *
 * It sits **below** the video rather than over it. Sixteen readings do not fit
 * in a player box a few hundred pixels tall — the first cut was a scrolling
 * postage stamp covering the thing it was describing — and the questions it
 * answers ("why is this transcoding?") get asked while looking at a page, not
 * while filling a screen. The cost is that it is not there in fullscreen;
 * leaving fullscreen brings it back.
 */

/** How often the live readings are re-sampled while the panel is open. */
const TICK = 500;

export interface PlaybackStatsProps {
  video: Video;
  el: HTMLVideoElement | null;
  caps: DeviceCapabilities;
  /** The gate's answer for this video, and why. */
  decision: { kind: "native" | "hls" | "audioOnly" | "unplayable"; reason: PlaybackReason; url: string | null };
  variant: HLSVariant | null;
  rendition: { state: HLSState | null; progress: number | null; preparing: boolean };
  preview: PreviewStatus;
  loudness: Loudness | undefined;
  loudnessEnabled: boolean;
  audioOnly: boolean;
  /** Where this playback began — the resume point, or 0. */
  startedAt: number;
  onClose: () => void;
}

export function PlaybackStats(props: PlaybackStatsProps) {
  const { video, el, caps, decision, variant, rendition, preview, loudness, loudnessEnabled, audioOnly, startedAt, onClose } = props;

  // The element's counters change with playback, not with React state, so
  // nothing would re-render them. Sampling twice a second is enough to watch a
  // buffer drain and cheap enough to leave running while the panel is up.
  const [, tick] = useState(0);
  useEffect(() => {
    const id = window.setInterval(() => tick((n) => n + 1), TICK);
    return () => window.clearInterval(id);
  }, []);

  const delivery = describeDelivery(decision.kind, decision.reason, audioOnly);
  const ahead = bufferedAhead(el);
  const dropped = describeDroppedFrames(el?.getVideoPlaybackQuality?.() ?? null);
  const sourceStreams = (video.streams ?? []).filter((s) => s.type === "video");
  const sourceHeight = Math.max(0, video.height, ...sourceStreams.map((s) => s.height));

  return (
    <section className="@container mt-3 rounded-2xl border border-hair bg-surface px-4 py-3 text-[12.5px] font-semibold">
      <div className="flex items-center justify-between gap-4 pb-1">
        <h2 className="text-[11px] font-extrabold uppercase tracking-[0.14em] text-muted-2">Playback stats</h2>
        <button onClick={onClose} aria-label="Close playback stats" className="text-muted-2 hover:text-ink">
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" aria-hidden="true">
            <path d="M5 5l14 14M19 5L5 19" />
          </svg>
        </button>
      </div>

      {/* Two columns only once the panel itself is wide enough for them — a
          container query, not a viewport one: the panel is as wide as the
          video, which on a desktop with the up-next rail open is nothing like
          the window. Splitting on the viewport gave two columns of about
          thirty characters, and every value wrapped down the page. */}
      <div className="grid gap-x-8 @[30rem]:grid-cols-2">
      <Group title="Delivery">
        <Row label="Path" value={delivery.label} strong />
        <Row label="Why" value={delivery.why} />
        <Row label="Source" value={describeStream(sourceHeight, sourceStreams[0]?.codec ?? "")} />
        {delivery.kind === "rendition" && (
          <Row label="Rendition" value={describeRendition(variant, rendition.state, rendition.progress)} />
        )}
        {rendition.preparing && <Row label="Waiting on" value="the first segment of the rendition" />}
        <Row label="URL" value={decision.url ?? "—"} wrap />
      </Group>

      <Group title="Derived">
        <Row label="Scrub preview" value={describePreview(preview, Boolean(video.preview_url))} />
        <Row label="Loudness" value={describeLoudness(loudness, loudnessEnabled)} />
      </Group>

      <Group title="Element">
        <Row label="State" value={el ? elementState(el.readyState, el.networkState) : "no element"} />
        <Row label="Picture" value={el ? describeSize(el) : "—"} />
        <Row label="Buffer ahead" value={ahead === null ? "—" : `${ahead.toFixed(1)}s`} />
        {dropped && <Row label="Dropped frames" value={dropped} />}
        <Row label="Position" value={`${fmtDuration(el?.currentTime ?? 0)} of ${fmtDuration(video.duration)}`} />
        <Row label="Started at" value={startedAt > 0 ? fmtDuration(startedAt) : "the beginning"} />
        <Row label="Volume" value={el ? `${Math.round(el.volume * 100)}%${el.muted ? " (muted)" : ""}` : "—"} />
      </Group>

      <Group title="This browser">
        <Row label="Decodes" value={describeDecoders(caps.decodes)} />
        <Row label="Screen" value={`${caps.screenHeight}px tall`} />
        <Row label="HLS" value={caps.nativeHLS ? "native" : "hls.js"} />
      </Group>
      </div>
    </section>
  );
}

/** `1280×720`, or what the element will admit to before it has a frame. */
function describeSize(el: HTMLVideoElement): string {
  if (el.videoWidth === 0) return "no picture yet";
  return `${el.videoWidth}×${el.videoHeight}`;
}

function Group({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="mt-2.5 border-t border-hair pt-2">
      <span className="text-[10.5px] font-extrabold uppercase tracking-[0.12em] text-muted-3">{title}</span>
      <dl className="mt-1 flex flex-col gap-[3px]">{children}</dl>
    </div>
  );
}

function Row({ label, value, strong, wrap }: { label: string; value: string; strong?: boolean; wrap?: boolean }) {
  return (
    <div className="flex items-baseline gap-3">
      <dt className="w-[5.75rem] flex-none text-muted-2">{label}</dt>
      <dd className={`min-w-0 flex-1 ${wrap ? "break-all" : ""} ${strong ? "font-extrabold" : ""}`}>{value}</dd>
    </div>
  );
}
