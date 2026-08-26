import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import type { Prefs, SubtitleTrack, Video } from "@/lib/api";
import { fmtDuration } from "@/lib/format";
import { retryMediaUrl } from "@/lib/media";
import { CheckIcon, Popover } from "@/components/ui";
import { useChapters } from "@/lib/queries";
import { useSponsorSkip } from "./useSponsorSkip";
import { useProgressHeartbeat } from "./useProgressHeartbeat";
import { Scrubber } from "./Scrubber";
import { currentChapterIndex, nextChapterStart, prevChapterStart } from "./chapterMath";

const SPEEDS = [0.75, 1, 1.25, 1.5, 1.75, 2];
const SIZES: Prefs["subtitle_size"][] = ["small", "medium", "large"];

export interface PlayerProps {
  video: Video;
  prefs: Prefs;
  startAt?: number; // explicit ?t= override
  onPrefs: (patch: Partial<Prefs>) => void;
  onWatched: () => void;
  onStartOver: () => Promise<void>;
  onEnded: () => void;
  /** Fires only when the current chapter changes (including to/from -1 = none). */
  onChapterChange?: (index: number) => void;
  /**
   * Step through the surrounding playlist/feed/channel. Present only when the
   * video was opened with a context; a handler is undefined at either end of
   * the list, which disables that button rather than hiding it so the control
   * bar doesn't shift as you move through.
   */
  nav?: { onPrev?: () => void; onNext?: () => void };
}

export interface PlayerHandle {
  seek: (time: number) => void;
}

// HTML5 player with custom controls per the Player artboard: resume chip,
// progress scrubber, play / ±10s, time, CC menu, speed menu, mute, fullscreen.
export const Player = forwardRef<PlayerHandle, PlayerProps>(function Player(
  { video, prefs, startAt, onPrefs, onWatched, onStartOver, onEnded, onChapterChange, nav },
  ref,
) {
  const [el, setEl] = useState<HTMLVideoElement | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [src, setSrc] = useState(video.media_url);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [duration, setDuration] = useState(video.duration);
  const [muted, setMuted] = useState(false);
  const [resumedFrom, setResumedFrom] = useState<number | null>(null);
  const [menu, setMenu] = useState<"cc" | "speed" | null>(null);
  const [ccAnchor, setCcAnchor] = useState<HTMLButtonElement | null>(null);
  const [speedAnchor, setSpeedAnchor] = useState<HTMLButtonElement | null>(null);
  const [idle, setIdle] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const idleTimer = useRef<number | undefined>(undefined);

  // Active subtitle track: prefs.subtitle_lang → matching archived track first,
  // then auto.
  const activeLang = prefs.subtitle_lang;
  const activeTrack = pickTrack(video.subtitles, activeLang);

  useEffect(() => {
    setSrc(video.media_url);
    setResumedFrom(null);
    setError(null);
  }, [video.id, video.media_url]);

  // Seek to resume point / ?t= once metadata is known; apply speed.
  useEffect(() => {
    if (!el) return;
    el.playbackRate = prefs.playback_speed || 1;
    const onMeta = () => {
      setDuration(el.duration || video.duration);
      // Resume is the default action from every entry point — the card, the
      // title and the Resume button all link here, so any saved position is
      // honoured (matching the "Resume · m:ss" pill the card shows).
      const resume = startAt ?? (!video.watched && video.position > 0 ? video.position : 0);
      if (resume > 0 && resume < (el.duration || video.duration) - 5) {
        el.currentTime = resume;
        if (startAt === undefined) setResumedFrom(resume);
      }
      void el.play().catch(() => {
        /* autoplay may be blocked; user presses play */
      });
    };
    if (el.readyState >= 1) onMeta();
    else el.addEventListener("loadedmetadata", onMeta, { once: true });
    return () => el.removeEventListener("loadedmetadata", onMeta);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [el, video.id, src]);

  useEffect(() => {
    if (el) el.playbackRate = prefs.playback_speed || 1;
  }, [el, prefs.playback_speed]);

  // Toggle native text tracks to match the chosen one.
  useEffect(() => {
    if (!el) return;
    const tracks = el.textTracks;
    for (let i = 0; i < tracks.length; i++) {
      const t = tracks[i];
      const on = activeTrack && t.language === activeTrack.lang && t.label === trackLabel(activeTrack);
      t.mode = on ? "showing" : "disabled";
    }
  }, [el, activeTrack, video.subtitles]);

  useSponsorSkip(el, video.sponsorblock, prefs.skip_sponsors);
  useProgressHeartbeat(el, video.id, onWatched);

  // Chapters degrade silently: an empty list or a failed request just means
  // no marks/title/list, never an error or a blocking spinner. Memoized so a
  // stable empty array doesn't retrigger effects/memos below every render.
  const chaptersData = useChapters(video.id).data?.chapters;
  const chapters = useMemo(() => chaptersData ?? [], [chaptersData]);
  const chapterIdx = useMemo(() => currentChapterIndex(chapters, time), [chapters, time]);
  const currentChapter = chapterIdx >= 0 ? chapters[chapterIdx] : null;
  useEffect(() => {
    onChapterChange?.(chapterIdx);
  }, [chapterIdx, onChapterChange]);

  const togglePlay = useCallback(() => {
    if (!el) return;
    if (el.paused) void el.play();
    else el.pause();
  }, [el]);
  const seekTo = useCallback(
    (t: number) => {
      if (el) el.currentTime = Math.max(0, Math.min(el.duration || duration, t));
    },
    [el, duration],
  );
  const seekBy = useCallback(
    (d: number) => {
      if (el) seekTo(el.currentTime + d);
    },
    [el, seekTo],
  );
  useImperativeHandle(ref, () => ({ seek: seekTo }), [seekTo]);
  const toggleFullscreen = useCallback(() => {
    const w = wrapRef.current;
    if (!w) return;
    if (document.fullscreenElement) void document.exitFullscreen();
    else void w.requestFullscreen?.();
  }, []);
  const toggleCC = useCallback(() => {
    if (activeTrack) onPrefs({ subtitle_lang: SUBTITLE_OFF });
    else {
      const first = video.subtitles[0];
      if (first) onPrefs({ subtitle_lang: first.lang });
    }
  }, [activeTrack, onPrefs, video.subtitles]);

  // Keyboard: space/k play, j/l ±10s, f fullscreen, c CC, m mute, arrows ±5s,
  // [ / ] previous/next chapter.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.isContentEditable)) return;
      switch (e.key) {
        case " ":
        case "k":
          e.preventDefault();
          togglePlay();
          break;
        case "j":
          seekBy(-10);
          break;
        case "l":
          seekBy(10);
          break;
        case "ArrowLeft":
          seekBy(-5);
          break;
        case "ArrowRight":
          seekBy(5);
          break;
        case "f":
          toggleFullscreen();
          break;
        case "c":
          toggleCC();
          break;
        case "m":
          if (el) {
            el.muted = !el.muted;
            setMuted(el.muted);
          }
          break;
        case "[": {
          const target = prevChapterStart(chapters, el?.currentTime ?? time);
          if (target !== null) seekTo(target);
          break;
        }
        case "]": {
          const target = nextChapterStart(chapters, el?.currentTime ?? time);
          if (target !== null) seekTo(target);
          break;
        }
        // Step through the surrounding list. Lower case only: shifted keys are
        // left alone so they stay available for text entry elsewhere.
        case "p":
          nav?.onPrev?.();
          break;
        case "n":
          nav?.onNext?.();
          break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [el, togglePlay, seekBy, toggleFullscreen, toggleCC, chapters, time, seekTo, nav]);

  const bumpIdle = () => {
    setIdle(false);
    window.clearTimeout(idleTimer.current);
    idleTimer.current = window.setTimeout(() => setIdle(true), 2500);
  };

  const showControls = !idle || !playing;

  return (
    <div
      ref={wrapRef}
      className="group relative aspect-video w-full overflow-hidden rounded-2xl bg-black text-white"
      onMouseMove={bumpIdle}
      onPointerDown={bumpIdle}
      style={{ cursor: showControls ? "default" : "none" }}
    >
      <video
        ref={setEl}
        src={src}
        className={`h-full w-full cc-${prefs.subtitle_size}`}
        playsInline
        preload="metadata"
        onClick={togglePlay}
        onPlay={() => setPlaying(true)}
        onPause={() => setPlaying(false)}
        onTimeUpdate={(e) => setTime(e.currentTarget.currentTime)}
        onDurationChange={(e) => setDuration(e.currentTarget.duration || video.duration)}
        onVolumeChange={(e) => setMuted(e.currentTarget.muted)}
        onEnded={onEnded}
        onError={() => {
          // Likely an expired media cookie: refresh it once and reload.
          void retryMediaUrl(video.media_url).then((next) => {
            if (next) setSrc(next);
            else setError("Could not load the video.");
          });
        }}
      >
        {video.subtitles.map((t) => (
          <track key={`${t.source}-${t.lang}`} kind="subtitles" srcLang={t.lang} label={trackLabel(t)} src={t.url} default={activeTrack === t} />
        ))}
      </video>

      {error && (
        <div className="absolute inset-0 flex items-center justify-center text-sm font-semibold text-white/80">{error}</div>
      )}

      {resumedFrom !== null && (
        <div className="absolute left-3.5 top-3.5 flex items-center gap-2 rounded-full bg-[rgba(23,24,26,0.85)] py-1.5 pl-3 pr-2.5 text-[12px] font-bold">
          <span>Resumed from {fmtDuration(resumedFrom)}</span>
          <button
            className="text-accent-soft"
            onClick={() => {
              void onStartOver().then(() => {
                if (el) el.currentTime = 0;
                setResumedFrom(null);
              });
            }}
          >
            Start over
          </button>
        </div>
      )}

      <div
        className={`absolute inset-x-0 bottom-0 flex flex-col gap-2.5 bg-gradient-to-t from-black/75 to-transparent px-4 pb-3 pt-6 transition-opacity ${showControls ? "opacity-100" : "opacity-0"}`}
      >
        <Scrubber time={time} duration={duration} chapters={chapters} sponsorblock={video.sponsorblock} onSeek={seekTo} />
        <div className="flex items-center gap-4 text-[12px] font-bold">
          {nav && (
            <button onClick={nav.onPrev} disabled={!nav.onPrev} aria-label="Previous video" className="disabled:opacity-35">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M7 5h2v14H7zM19 5v14l-9-7z" /></svg>
            </button>
          )}
          <button onClick={togglePlay} aria-label={playing ? "Pause" : "Play"}>
            {playing ? (
              <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor"><path d="M6 4h4v16H6zM14 4h4v16h-4z" /></svg>
            ) : (
              <svg width="22" height="22" viewBox="0 0 24 24" fill="currentColor"><path d="M7 4l12 8-12 8z" /></svg>
            )}
          </button>
          <button onClick={() => seekBy(-10)} aria-label="Back 10 seconds">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 12a8 8 0 1 1 3 6.2" /><path d="M4 18v-6h6" /></svg>
          </button>
          <button onClick={() => seekBy(10)} aria-label="Forward 10 seconds">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M20 12a8 8 0 1 0-3 6.2" /><path d="M20 18v-6h-6" /></svg>
          </button>
          {nav && (
            <button onClick={nav.onNext} disabled={!nav.onNext} aria-label="Next video" className="disabled:opacity-35">
              <svg width="20" height="20" viewBox="0 0 24 24" fill="currentColor"><path d="M15 5h2v14h-2zM5 5l9 7-9 7z" /></svg>
            </button>
          )}
          <span className="tabular-nums">
            {fmtDuration(time)} / {fmtDuration(duration)}
          </span>
          {currentChapter && (
            <span className="min-w-0 max-w-[160px] truncate text-white/70 sm:max-w-[300px]" title={currentChapter.title}>
              · {currentChapter.title}
            </span>
          )}
          <button
            ref={setCcAnchor}
            onClick={() => setMenu(menu === "cc" ? null : "cc")}
            aria-label="Subtitles"
            aria-expanded={menu === "cc"}
            className={`ml-auto rounded px-[7px] py-[3px] text-[11px] font-extrabold ${activeTrack ? "bg-white text-[#17181a]" : "border border-white/60"}`}
          >
            CC
          </button>
          <button ref={setSpeedAnchor} onClick={() => setMenu(menu === "speed" ? null : "speed")} aria-label="Playback speed" aria-expanded={menu === "speed"}>
            {fmtSpeed(prefs.playback_speed)}
          </button>
          <button
            onClick={() => {
              if (el) {
                el.muted = !el.muted;
                setMuted(el.muted);
              }
            }}
            aria-label={muted ? "Unmute" : "Mute"}
          >
            {muted ? (
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9h4l5-4v14l-5-4H4z" /><path d="M17 9l4 6M21 9l-4 6" /></svg>
            ) : (
              <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9h4l5-4v14l-5-4H4z" /><path d="M16 9a4 4 0 0 1 0 6" /></svg>
            )}
          </button>
          <button onClick={toggleFullscreen} aria-label="Fullscreen">
            <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" /></svg>
          </button>
        </div>
      </div>

      {menu === "cc" && (
        <Popover anchor={ccAnchor} onClose={() => setMenu(null)} width={220}>
          <div className="pop">
            <span className="sec px-2.5 pb-1 pt-1.5 !text-muted-3">Subtitles</span>
            <button className={`pop-item ${!activeTrack ? "on" : ""}`} onClick={() => onPrefs({ subtitle_lang: SUBTITLE_OFF })}>
              <span>Off</span>
              {!activeTrack && <CheckIcon size={14} />}
            </button>
            {video.subtitles.map((t) => (
              <button key={`${t.source}-${t.lang}`} className={`pop-item ${activeTrack === t ? "on" : ""}`} onClick={() => onPrefs({ subtitle_lang: t.lang })}>
                <span>{langName(t.lang)}</span>
                {activeTrack === t ? <CheckIcon size={14} /> : t.source === "auto" ? <span className="text-[11px] text-[#c9c6bd]">auto</span> : null}
              </button>
            ))}
            {video.subtitles.length === 0 && <span className="px-2.5 py-2 text-[#c9c6bd]">No subtitles archived</span>}
            <div className="mt-1 flex items-center justify-between border-t border-white/15 px-2.5 pt-2.5 text-[#c9c6bd]">
              <span>Size</span>
              <span className="flex gap-1">
                {SIZES.map((s) => (
                  <button key={s} onClick={() => onPrefs({ subtitle_size: s })} className={`rounded px-1.5 py-0.5 capitalize ${prefs.subtitle_size === s ? "bg-white/15 text-white" : ""}`}>
                    {s}
                  </button>
                ))}
              </span>
            </div>
          </div>
        </Popover>
      )}
      {menu === "speed" && (
        <Popover anchor={speedAnchor} onClose={() => setMenu(null)} width={160}>
          <div className="pop">
            <span className="sec px-2.5 pb-1 pt-1.5 !text-muted-3">Speed</span>
            {SPEEDS.map((s) => (
              <button key={s} className={`pop-item ${prefs.playback_speed === s ? "on" : ""}`} onClick={() => onPrefs({ playback_speed: s })}>
                <span>{fmtSpeed(s)}</span>
                {prefs.playback_speed === s && <CheckIcon size={14} />}
              </button>
            ))}
          </div>
        </Popover>
      )}
    </div>
  );
});

export const SUBTITLE_OFF = "off";

// Resolve the preferred language to a track: exact match first (archived
// before auto-generated), then the same base language ("en" matches "en-US").
export function pickTrack(tracks: SubtitleTrack[], lang: string | null): SubtitleTrack | null {
  if (!lang || lang === SUBTITLE_OFF) return null;
  const base = lang.split("-")[0].toLowerCase();
  const sameBase = (t: SubtitleTrack) => t.lang.split("-")[0].toLowerCase() === base;
  return (
    tracks.find((t) => t.lang === lang && t.source === "user") ??
    tracks.find((t) => t.lang === lang) ??
    tracks.find((t) => sameBase(t) && t.source === "user") ??
    tracks.find(sameBase) ??
    null
  );
}

export function trackLabel(t: SubtitleTrack): string {
  return t.source === "auto" ? `${langName(t.lang)} (auto)` : langName(t.lang);
}

export function fmtSpeed(s: number): string {
  return `${Number.isInteger(s) ? s : s.toFixed(2).replace(/0$/, "")}×`;
}

const names: Record<string, string> = { en: "English", de: "Deutsch", fr: "Français", es: "Español", it: "Italiano", pt: "Português", nl: "Nederlands", ja: "日本語", ko: "한국어", zh: "中文", ru: "Русский" };
export function langName(code: string): string {
  const base = code.toLowerCase().split("-")[0];
  try {
    return names[base] ?? new Intl.DisplayNames([navigator.language], { type: "language" }).of(code) ?? code.toUpperCase();
  } catch {
    return names[base] ?? code.toUpperCase();
  }
}
