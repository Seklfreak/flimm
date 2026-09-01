import { Clamp } from "./Clamp";
import { RichText } from "./RichText";

export interface DescriptionProps {
  text: string;
  duration: number;
  onSeek: (seconds: number) => void;
}

// The video's description, folded to a few lines the way the phone folds it:
// a description is a paragraph and a wall of links, and the wall goes under
// the fold. Timestamps in it seek, which is what a chapter list in prose is
// for, and URLs open in a new tab.
export function Description({ text, duration, onSeek }: DescriptionProps) {
  if (!text.trim()) return null;
  return (
    <Clamp lines={4} className="rounded-[14px] bg-raised-2 p-4">
      <p className="whitespace-pre-wrap text-[15px] leading-[1.6] text-ink [overflow-wrap:anywhere]">
        <RichText text={text} duration={duration} onSeek={onSeek} />
      </p>
    </Clamp>
  );
}
