import { forwardRef, useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from "react";
import type { HLSState, Prefs, SubtitleTrack, Video } from "@/lib/api";
import { fmtDuration } from "@/lib/format";
import { refreshMediaSession, retryMediaUrl } from "@/lib/media";
import { CheckIcon, HeadphonesIcon, MediaImg, Popover, Spinner } from "@/components/ui";
import { useChapters } from "@/lib/queries";
import { useSponsorSkip } from "./useSponsorSkip";
import { useProgressHeartbeat } from "./useProgressHeartbeat";
import { useRendition } from "./useRendition";
import { Scrubber } from "./Scrubber";
import { currentChapterIndex, highlightToOffer, nextChapterStart, prevChapterStart } from "./chapterMath";
import { HLS_CONFIG, loadHls } from "./hls";
import {
  archivePlays,
  canPlayVariant,
  codecFamily,
  codecLabel,
  decide,
  detectCapabilities,
  loadQuality,
  qualityLabel,
  saveQuality,
  videoStreams,
  type DeviceCapabilities,
  type QualityPreference,
} from "./codecGate";

const SPEEDS = [0.75, 1, 1.25, 1.5, 1.75, 2];
const SIZES: Prefs["subtitle_size"][] = ["small", "medium", "large"];
/** How many fatal network errors to ride out before giving up on a rendition.
 *  A playlist the server cannot open yet answers 503 + Retry-After, and a
 *  segment the encoder has not reached blocks for up to a minute — both are
 *  "come back", not "broken". */
const HLS_NETWORK_RETRIES = 8;

/** Append `?from=<seconds>` to an HLS playlist URL — see `playerFrom` below. */
function withFrom(url: string, from: number): string {
  return `${url}${url.includes("?") ? "&" : "?"}from=${from}`;
}

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
  /** `audio=1` in the URL — the live audio/video mode for this video. */
  audioOnly: boolean;
  /** Flips the mode; the caller owns persisting it into the URL. */
  onToggleAudioOnly: () => void;
  /**
   * The playlist this video is being played *from* (the play context), so
   * progress heartbeats can carry it — the server needs it to recognize
   * playback from a music playlist and skip recording watch state.
   */
  playlistId?: string;
}

export interface PlayerHandle {
  seek: (time: number) => void;
}

// HTML5 player with custom controls per the Player artboard: resume chip,
// progress scrubber, play / ±10s, time, CC menu, speed menu, mute, fullscreen.
export const Player = forwardRef<PlayerHandle, PlayerProps>(function Player(
  { video, prefs, startAt, onPrefs, onWatched, onStartOver, onEnded, onChapterChange, nav, audioOnly, onToggleAudioOnly, playlistId },
  ref,
) {
  const [el, setEl] = useState<HTMLVideoElement | null>(null);
  const wrapRef = useRef<HTMLDivElement>(null);
  const [playing, setPlaying] = useState(false);
  const [time, setTime] = useState(0);
  const [duration, setDuration] = useState(video.duration);
  const [muted, setMuted] = useState(false);
  const [resumedFrom, setResumedFrom] = useState<number | null>(null);
  const [menu, setMenu] = useState<"cc" | "speed" | "quality" | null>(null);
  const [ccAnchor, setCcAnchor] = useState<HTMLButtonElement | null>(null);
  const [speedAnchor, setSpeedAnchor] = useState<HTMLButtonElement | null>(null);
  const [qualityAnchor, setQualityAnchor] = useState<HTMLButtonElement | null>(null);
  const [idle, setIdle] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const idleTimer = useRef<number | undefined>(undefined);
  const prevAudioOnlyRef = useRef(audioOnly);

  // ---- what plays here ------------------------------------------------------
  // What this browser decodes and how tall a picture it can show. Probed once:
  // it cannot change while the tab is open, and each probe builds an element.
  const [caps] = useState<DeviceCapabilities>(detectCapabilities);
  // Per device, never per account (see codecGate) — an old laptop and a 4K
  // desktop want different answers from the same login.
  const [quality, setQuality] = useState<QualityPreference>(loadQuality);
  const decision = useMemo(() => decide(video, quality, audioOnly, caps), [video, quality, audioOnly, caps]);
  const isHls = decision.kind === "hls";
  const sourceUrl = decision.kind === "native" || decision.kind === "hls" ? decision.url : null;
  const variant = decision.kind === "hls" ? decision.variant : null;
  // A cache-busted retry URL after a media 401; cleared whenever the source
  // itself changes.
  const [retryUrl, setRetryUrl] = useState<string | null>(null);

  // Where playback should begin for the source being loaded: the saved resume
  // position on the first load, and the clock the viewer was at when they
  // switched audio mode or quality. It is also the `from` the encoder is
  // aimed at, so a resume at 40:00 transcodes 40:00 first.
  const startPosRef = useRef<number>(startAt ?? (!video.watched && video.position > 0 ? video.position : 0));
  const firstLoadRef = useRef(true);

  // The URL handed to the player. A rendition carries `?from=<pos>` so the
  // server returns a media playlist with `#EXT-X-START` and the player begins
  // at the resume point — fetching the resume segment first (the transcode
  // produces it first) instead of blocking on seg 0. `startPosRef` is already
  // the clock the viewer was at on a quality switch, so the switched-to
  // rendition starts there too. The plain archive path takes no `from`.
  const playerFrom = isHls ? Math.floor(startPosRef.current) : 0;
  const playerSource = sourceUrl && playerFrom > 0 ? withFrom(sourceUrl, playerFrom) : sourceUrl;
  const url = retryUrl ?? playerSource;

  const rendition = useRendition({
    videoId: video.id,
    active: isHls,
    height: variant?.height ?? null,
    from: startPosRef.current,
    el,
  });

  // Active subtitle track: prefs.subtitle_lang → matching archived track first,
  // then auto.
  const activeLang = prefs.subtitle_lang;
  const activeTrack = pickTrack(video.subtitles, activeLang);

  // The rungs worth offering: the ones this browser can decode. A machine
  // without an HEVC decoder is not shown 1440p and 2160p, because picking one
  // would only fall back to a shorter rung anyway.
  const ladder = useMemo(
    () => (video.hls_variants ?? []).filter((v) => canPlayVariant(v.codec, caps)).sort((a, b) => b.height - a.height),
    [video.hls_variants, caps],
  );
  const sourceStreams = useMemo(() => videoStreams(video), [video]);
  const sourceHeight = Math.max(0, video.height, ...sourceStreams.map((st) => st.height));
  const sourceCodec = sourceStreams[0]?.codec ?? "";
  const sourcePlays = archivePlays(video, caps);

  useEffect(() => {
    if (prevAudioOnlyRef.current !== audioOnly && el) startPosRef.current = el.currentTime;
    prevAudioOnlyRef.current = audioOnly;
    setResumedFrom(null);
    setRetryUrl(null);
    setError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [video.id, video.media_url, video.audio_url, audioOnly]);

  // Attach the source. One effect owns the element's src for both paths:
  // hls.js swaps in a blob URL of its own, so leaving `src` as a React prop
  // would have the two clobber each other on the next render.
  //
  // Safari plays HLS natively, so it stays on the plain assignment and never
  // downloads hls.js; everywhere else the library is imported the first time a
  // rendition is actually chosen. Either way the transcode is started first
  // (`rendition.started`), so the encoder is already aimed at the resume
  // position when the first segment is asked for.
  useEffect(() => {
    if (!el || !url) return;
    if (isHls && !rendition.started) return;
    if (!isHls || caps.nativeHLS) {
      el.src = url;
      return;
    }
    let destroyed = false;
    let hls: { destroy: () => void; startLoad: () => void; recoverMediaError: () => void } | null = null;
    let networkRetries = 0;
    let retriedAuth = false;
    let retryTimer: number | undefined;
    void loadHls()
      .then((Hls) => {
        if (destroyed) return;
        if (!Hls.isSupported()) {
          setError("This browser can't play the compatible version.");
          return;
        }
        // `startPosition` is belt-and-suspenders for the EXT-X-START the media
        // playlist already carries: it makes hls.js request the resume segment
        // first even before it has parsed the playlist. -1 is its default
        // (start at the beginning) for a start-over.
        const instance = new Hls({ ...HLS_CONFIG, startPosition: playerFrom > 0 ? playerFrom : -1 });
        hls = instance;
        instance.on(Hls.Events.ERROR, (_event, data) => {
          if (!data.fatal) return;
          // The media cookie expired: refresh it once and pick up where the
          // loader stopped, exactly as the direct path does.
          if (data.response?.code === 401 && !retriedAuth) {
            retriedAuth = true;
            void refreshMediaSession().then(() => {
              if (!destroyed) instance.startLoad();
            });
            return;
          }
          if (data.type === Hls.ErrorTypes.NETWORK_ERROR && networkRetries < HLS_NETWORK_RETRIES) {
            networkRetries += 1;
            retryTimer = window.setTimeout(() => {
              if (!destroyed) instance.startLoad();
            }, 2_000);
            return;
          }
          if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
            instance.recoverMediaError();
            return;
          }
          setError("Could not load the compatible version.");
        });
        instance.loadSource(url);
        instance.attachMedia(el);
      })
      .catch(() => setError("Could not load the compatible version."));
    return () => {
      destroyed = true;
      window.clearTimeout(retryTimer);
      hls?.destroy();
    };
  }, [el, url, isHls, caps.nativeHLS, rendition.started, playerFrom]);

  // Seek to resume point / ?t= once metadata is known; apply speed.
  useEffect(() => {
    if (!el) return;
    el.playbackRate = prefs.playback_speed || 1;
    const onMeta = () => {
      const dur = el.duration || video.duration;
      setDuration(dur);
      const t = startPosRef.current;
      const first = firstLoadRef.current;
      firstLoadRef.current = false;
      // Resume is the default action from every entry point — the card, the
      // title and the Resume button all link here, so any saved position is
      // honoured (matching the "Resume · m:ss" pill the card shows). A
      // reload after an audio or quality switch resumes the clock it was at
      // instead, which is why the tail margin is tighter there.
      if (t > 0 && t < dur - (first ? 5 : 0.5)) {
        el.currentTime = t;
        if (first && startAt === undefined) setResumedFrom(t);
      }
      void el.play().catch(() => {
        /* autoplay may be blocked; user presses play */
      });
    };
    if (el.readyState >= 1) onMeta();
    else el.addEventListener("loadedmetadata", onMeta, { once: true });
    return () => el.removeEventListener("loadedmetadata", onMeta);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [el, video.id, url, isHls]);

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
  const highlight = highlightToOffer(video.sponsorblock, time);
  useProgressHeartbeat(el, video.id, onWatched, playlistId);

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
  // Switching quality is a reload of a different source, so remember the clock
  // first: the new one is seeked back to it as soon as it has metadata, and
  // the encoder is aimed there rather than at 0:00.
  const changeQuality = useCallback(
    (q: QualityPreference) => {
      if (el) startPosRef.current = el.currentTime;
      firstLoadRef.current = false;
      setQuality(q);
      saveQuality(q);
      setRetryUrl(null);
      setError(null);
      setMenu(null);
    },
    [el],
  );

  // Media Session: lock-screen / hardware-key controls while playing audio
  // only. navigator.mediaSession is missing in some browsers (and in jsdom),
  // so every access is guarded and never allowed to throw. The previous
  // effect run's cleanup clears the handlers when audioOnly turns off or the
  // component unmounts.
  useEffect(() => {
    const ms = typeof navigator !== "undefined" ? navigator.mediaSession : undefined;
    if (!ms) return;
    if (audioOnly) {
      try {
        if (typeof MediaMetadata !== "undefined") {
          ms.metadata = new MediaMetadata({
            title: video.title,
            artist: video.channel.name,
            artwork: video.thumb_url ? [{ src: video.thumb_url }] : [],
          });
        }
        ms.setActionHandler("play", () => {
          void el?.play().catch(() => {});
        });
        ms.setActionHandler("pause", () => el?.pause());
        ms.setActionHandler("seekbackward", () => seekBy(-10));
        ms.setActionHandler("seekforward", () => seekBy(10));
        ms.setActionHandler("previoustrack", () => nav?.onPrev?.());
        ms.setActionHandler("nexttrack", () => nav?.onNext?.());
      } catch {
        /* an unsupported action or metadata shape in this browser */
      }
    }
    return () => {
      try {
        ms.metadata = null;
        ms.setActionHandler("play", null);
        ms.setActionHandler("pause", null);
        ms.setActionHandler("seekbackward", null);
        ms.setActionHandler("seekforward", null);
        ms.setActionHandler("previoustrack", null);
        ms.setActionHandler("nexttrack", null);
      } catch {
        /* ignore */
      }
    };
  }, [audioOnly, video.title, video.channel.name, video.thumb_url, el, seekBy, nav]);

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
      {/* No `src` prop: the effect above owns the element's source, because
          hls.js assigns one of its own and React would fight it. */}
      <video
        ref={setEl}
        className={`h-full w-full cc-${prefs.subtitle_size} ${audioOnly ? "invisible" : ""}`}
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
          // hls.js reports its own errors (and clears the element's src while
          // recovering), so only the direct path answers here.
          if (isHls && !caps.nativeHLS) return;
          // Likely an expired media cookie: refresh it once and reload. Retry
          // the `?from`-bearing URL so a native-HLS resume keeps its offset.
          if (!playerSource) return;
          void retryMediaUrl(playerSource).then((next) => {
            if (next) setRetryUrl(next);
            else setError(audioOnly ? "Could not load the audio." : "Could not load the video.");
          });
        }}
      >
        {video.subtitles.map((t) => (
          <track key={`${t.source}-${t.lang}`} kind="subtitles" srcLang={t.lang} label={trackLabel(t)} src={t.url} default={activeTrack === t} />
        ))}
      </video>

      {/* Same aspect box as the video, so toggling audio mode never reflows
          the page — only what's painted inside it changes. */}
      {audioOnly && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-4 px-6 text-center" onClick={togglePlay}>
          <div className="aspect-square h-full max-h-[65%] overflow-hidden rounded-2xl bg-thumb shadow-modal">
            {video.thumb_url && <MediaImg src={video.thumb_url} alt="" className="h-full w-full object-cover" />}
          </div>
          <div className="flex max-w-md flex-col gap-1">
            <p className="truncate text-[16px] font-extrabold text-white">{video.title}</p>
            <p className="truncate text-[13px] font-semibold text-white/70">{video.channel.name}</p>
          </div>
        </div>
      )}

      {error && (
        <div className="absolute inset-0 flex items-center justify-center text-sm font-semibold text-white/80">{error}</div>
      )}

      {/* The codec wall. Only a server with no compatible rendition at all
          gets here — otherwise the gate has already picked one. */}
      {!error && (decision.kind === "audioOnly" || decision.kind === "unplayable") && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 px-8 text-center">
          <p className="text-sm font-semibold text-white/85">
            This video's codec ({decision.issue.videoCodec}) can't be played in this browser.
          </p>
          {decision.issue.audioAvailable ? (
            <button className="btn pri" onClick={onToggleAudioOnly}>
              <HeadphonesIcon size={14} />
              Play audio only
            </button>
          ) : (
            <p className="text-[13px] font-medium text-white/60">There is no compatible version to fall back to.</p>
          )}
        </div>
      )}

      {/* A rendition that has produced nothing yet. The playlist is a complete
          VOD list from the first request, so this comes down as soon as there
          is a frame — long before the transcode finishes. */}
      {!error && isHls && rendition.preparing && (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 text-center">
          <Spinner />
          <p className="text-[13px] font-semibold text-white/80">
            Preparing a compatible version…
            {rendition.progress !== null ? ` ${Math.round(rendition.progress * 100)}%` : ""}
          </p>
        </div>
      )}

      {/* "Jump to the highlight": a SponsorBlock point of interest is offered,
          never taken for the viewer, and offered whatever `skip_sponsors`
          says — this is a click, not a skip. It goes as soon as playback
          reaches it, and does not wait for the controls: a highlight nobody
          sees is a highlight nobody uses. */}
      {highlight && (
        <button
          className="absolute right-3.5 top-3.5 flex items-center gap-1.5 rounded-full bg-[rgba(23,24,26,0.85)] py-1.5 pl-3 pr-3 text-[12px] font-bold"
          onClick={() => seekTo(highlight.start)}
        >
          <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
            <path d="M12 2l2.2 5.8L20 10l-5.8 2.2L12 18l-2.2-5.8L4 10l5.8-2.2z" />
          </svg>
          <span>Jump to the highlight</span>
          <span className="font-semibold text-white/60">{fmtDuration(highlight.start)}</span>
        </button>
      )}

      {resumedFrom !== null && (
        <div className="absolute left-3.5 top-3.5 flex items-center gap-2 rounded-full bg-[rgba(23,24,26,0.85)] py-1.5 pl-3 pr-2.5 text-[12px] font-bold">
          <span>Resumed from {fmtDuration(resumedFrom)}</span>
          <button
            className="text-accent-soft"
            onClick={() => {
              void onStartOver().then(() => {
                startPosRef.current = 0;
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
          <div className="ml-auto flex items-center gap-4">
            {!audioOnly && (
              <button
                ref={setCcAnchor}
                onClick={() => setMenu(menu === "cc" ? null : "cc")}
                aria-label="Subtitles"
                aria-expanded={menu === "cc"}
                className={`rounded px-[7px] py-[3px] text-[11px] font-extrabold ${activeTrack ? "bg-white text-[#17181a]" : "border border-white/60"}`}
              >
                CC
              </button>
            )}
            <button ref={setSpeedAnchor} onClick={() => setMenu(menu === "speed" ? null : "speed")} aria-label="Playback speed" aria-expanded={menu === "speed"}>
              {fmtSpeed(prefs.playback_speed)}
            </button>
            {!audioOnly && ladder.length > 0 && (
              <button
                ref={setQualityAnchor}
                onClick={() => setMenu(menu === "quality" ? null : "quality")}
                aria-label="Video quality"
                aria-expanded={menu === "quality"}
              >
                {qualityLabel(quality)}
              </button>
            )}
            <button
              onClick={() => {
                if (el) startPosRef.current = el.currentTime;
                firstLoadRef.current = false;
                onToggleAudioOnly();
              }}
              aria-label={audioOnly ? "Switch to video" : "Switch to audio only"}
              aria-pressed={audioOnly}
              className={`rounded px-[7px] py-[3px] ${audioOnly ? "bg-white text-[#17181a]" : ""}`}
            >
              <HeadphonesIcon size={16} />
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
            {!audioOnly && (
              <button onClick={toggleFullscreen} aria-label="Fullscreen">
                <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5" /></svg>
              </button>
            )}
          </div>
        </div>
      </div>

      {!audioOnly && menu === "cc" && (
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
      {!audioOnly && menu === "quality" && (
        <Popover anchor={qualityAnchor} onClose={() => setMenu(null)} width={230}>
          <div className="pop">
            <span className="sec px-2.5 pb-1 pt-1.5 !text-muted-3">Quality</span>
            <button className={`pop-item ${quality === "auto" ? "on" : ""}`} onClick={() => changeQuality("auto")}>
              <span>Auto</span>
              {quality === "auto" && <CheckIcon size={14} />}
            </button>
            {/* What Auto plays when the archive decodes here: the original
                file, full quality, and nothing for the server to transcode.
                A label, not a choice — "Auto" above already is it. */}
            {sourcePlays && (
              <span className="px-2.5 pb-1 pl-5 text-[11px] text-[#c9c6bd]">
                Source{sourceHeight > 0 ? ` · ${sourceHeight}p` : ""}
                {sourceCodec ? ` · ${codecLabel(sourceCodec)}` : ""}
              </span>
            )}
            {ladder.map((v) => (
              <button key={v.height} className={`pop-item ${quality === v.height ? "on" : ""}`} onClick={() => changeQuality(v.height)}>
                <span>
                  {v.height}p{codecFamily(v.codec) === "hevc" ? " · HEVC" : ""}
                </span>
                <span className="flex items-center gap-1.5">
                  {variantHint(v.state) && <span className="text-[11px] text-[#c9c6bd]">{variantHint(v.state)}</span>}
                  {quality === v.height && <CheckIcon size={14} />}
                </span>
              </button>
            ))}
            <span className="mt-1 block border-t border-white/15 px-2.5 pt-2 text-[11px] text-[#c9c6bd]">
              Kept on this device only.
            </span>
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

/**
 * The rung's own state as a one-word hint. `pending` is the normal state of
 * most of the ladder — nothing has asked for it — so it says nothing rather
 * than implying something is wrong.
 */
export function variantHint(state: HLSState): string {
  switch (state) {
    case "done":
      return "ready";
    case "running":
      return "preparing";
    case "failed":
      return "failed";
    default:
      return "";
  }
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
