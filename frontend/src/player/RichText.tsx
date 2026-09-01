import { useMemo } from "react";
import { segments } from "@/lib/richText";

export interface RichTextProps {
  text: string;
  /** The video's length, so a timestamp past the end stays text. */
  duration?: number;
  /** Without it timestamps are text too: nothing here can seek. */
  onSeek?: (seconds: number) => void;
}

// A description or a comment as it reads: URLs open in a new tab, timestamps
// seek the player, everything else is the text as written. The splitting is
// `segments` in lib/richText, shared with nothing else on purpose — it is the
// one place the rules live.
export function RichText({ text, duration, onSeek }: RichTextProps) {
  const parts = useMemo(() => segments(text, duration), [text, duration]);
  return (
    <>
      {parts.map((s, i) => {
        if (s.kind === "link") {
          return (
            <a
              key={i}
              href={s.href}
              target="_blank"
              rel="noopener noreferrer"
              className="font-semibold text-accent underline decoration-accent/40 underline-offset-2 hover:decoration-accent-deep"
            >
              {s.text}
            </a>
          );
        }
        if (s.kind === "time" && onSeek) {
          const seconds = s.seconds;
          return (
            <button
              key={i}
              type="button"
              className="rounded-[4px] bg-accent/10 px-1 font-semibold tabular-nums text-accent hover:bg-accent/20"
              onClick={() => onSeek(seconds)}
            >
              {s.text}
            </button>
          );
        }
        return <span key={i}>{s.text}</span>;
      })}
    </>
  );
}
