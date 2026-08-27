import { useEffect, useRef, useState } from "react";
import { api, type HLSState } from "@/lib/api";

/** How far a seek must land from where the encoder is aimed to re-aim it. */
const RESEEK_THRESHOLD = 30;
/** Dragging a scrubber is dozens of seeks; only where it settles counts. */
const RESEEK_DEBOUNCE = 1_000;
/** How often to ask again while there is nothing to show. */
const POLL_INTERVAL = 5_000;

export interface RenditionStatus {
  /** True once the transcode has been started (or the attempt has failed) and
   *  the playlist may be handed to the player. */
  started: boolean;
  /** True while the rendition has produced nothing playable yet. */
  preparing: boolean;
  /** 0–1, or null when the server has not said. Never shown at 0: a job that
   *  has reported nothing reads as stuck. */
  progress: number | null;
  state: HLSState | null;
}

/**
 * Starts the compatible rendition, keeps the encoder aimed where the viewer
 * is, and reports how far it has got.
 *
 * `POST /videos/{id}/hls?height=&from=` runs *before* the playlist is loaded,
 * so the encoder produces the part that is about to be watched first instead
 * of the forty minutes nobody is going to see. It is idempotent, which is why
 * the same call doubles as the progress poll and as the re-aim after a seek.
 *
 * It deliberately does not model which segments exist: the server knows that,
 * and a `from` it does not need is ignored. `hls_progress` is a number to
 * count up in the overlay, not one to infer a produced region from.
 */
export function useRendition(opts: {
  videoId: string;
  /** True when a rendition is what is playing. */
  active: boolean;
  /** The rung's height, or null on a server that offers only `hls_url`. */
  height: number | null;
  /** Where playback will start — the resume position, or the clock the viewer
   *  was at when they switched quality. */
  from: number;
  el: HTMLVideoElement | null;
}): RenditionStatus {
  const { videoId, active, height, from, el } = opts;
  const [started, setStarted] = useState(!active);
  const [preparing, setPreparing] = useState(active);
  const [progress, setProgress] = useState<number | null>(null);
  const [state, setState] = useState<HLSState | null>(null);

  // Reset during render rather than in the effect below. The caller attaches
  // the source in an effect of its own, and an effect cannot see state another
  // effect has only just queued: a switch to a rendition would attach the
  // playlist on the same commit that starts the transcode, and the encoder
  // would begin at 0:00 instead of where the viewer is.
  const key = `${videoId}|${height ?? ""}|${active}`;
  const [prevKey, setPrevKey] = useState(key);
  if (prevKey !== key) {
    setPrevKey(key);
    setStarted(!active);
    setPreparing(active);
    setProgress(null);
    setState(null);
  }

  // `from` is the aim at the moment the rendition starts, not a dependency:
  // changing it mid-play is a seek, which is steered below.
  const fromRef = useRef(from);
  fromRef.current = from;
  const aimRef = useRef(0);
  const doneRef = useRef(false);
  const playableRef = useRef(false);

  useEffect(() => {
    if (!active) return;
    let cancelled = false;
    let timer: number | undefined;
    doneRef.current = false;
    playableRef.current = false;
    aimRef.current = fromRef.current;

    const apply = (s: { state: HLSState; hls_progress: number }) => {
      if (cancelled) return;
      setState(s.state);
      setProgress(s.hls_progress > 0 ? s.hls_progress : null);
      if (s.state === "done") {
        doneRef.current = true;
        window.clearInterval(timer);
      }
    };

    api
      .startHLS(videoId, height ?? undefined, aimRef.current)
      .then(apply)
      .catch(() => {
        // The playlist request starts the job by itself, so a failed prefetch
        // is a slower start, not a dead end. Load it anyway.
      })
      .finally(() => {
        if (!cancelled) setStarted(true);
      });

    timer = window.setInterval(() => {
      if (playableRef.current || doneRef.current) {
        window.clearInterval(timer);
        return;
      }
      api
        .startHLS(videoId, height ?? undefined, aimRef.current)
        .then(apply)
        .catch(() => {
          /* the next tick asks again */
        });
    }, POLL_INTERVAL);

    return () => {
      cancelled = true;
      window.clearInterval(timer);
    };
  }, [videoId, height, active]);

  // The overlay comes down the moment there is a frame, whatever the encoder
  // has reached — the playlist is complete from the first request, so
  // playback and the transcode are not the same clock.
  useEffect(() => {
    if (!el || !active) return;
    const onPlayable = () => {
      playableRef.current = true;
      setPreparing(false);
    };
    el.addEventListener("canplay", onPlayable);
    el.addEventListener("playing", onPlayable);
    return () => {
      el.removeEventListener("canplay", onPlayable);
      el.removeEventListener("playing", onPlayable);
    };
  }, [el, active]);

  // A seek the viewer makes re-points the encoder, so the segments around the
  // new position are made next. Skipped once the rendition is finished, and
  // when the target is roughly where the encoder is already aimed.
  useEffect(() => {
    if (!el || !active) return;
    let debounce: number | undefined;
    const onSeeked = () => {
      if (doneRef.current) return;
      const t = el.currentTime;
      if (Math.abs(t - aimRef.current) <= RESEEK_THRESHOLD) return;
      window.clearTimeout(debounce);
      debounce = window.setTimeout(() => {
        if (doneRef.current) return;
        aimRef.current = t;
        api
          .startHLS(videoId, height ?? undefined, t)
          .then((s) => {
            setState(s.state);
            setProgress(s.hls_progress > 0 ? s.hls_progress : null);
            if (s.state === "done") doneRef.current = true;
          })
          .catch(() => {
            /* the segment request re-aims it anyway, just later */
          });
      }, RESEEK_DEBOUNCE);
    };
    el.addEventListener("seeked", onSeeked);
    return () => {
      window.clearTimeout(debounce);
      el.removeEventListener("seeked", onSeeked);
    };
  }, [el, active, videoId, height]);

  return { started, preparing, progress, state };
}
