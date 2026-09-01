import { useLayoutEffect, useRef, useState, type ReactNode } from "react";

export interface ClampProps {
  /** Lines shown before the fold. */
  lines: number;
  children: ReactNode;
  className?: string;
  more?: string;
  less?: string;
}

// Text folded to a few lines, with a control to unfold it — and no control at
// all when nothing was folded. The fold is CSS line clamping; whether it bit
// is measured, because a three-line description with a "Show more" that shows
// nothing more is a control that lies.
export function Clamp({ lines, children, className = "", more = "Show more", less = "Show less" }: ClampProps) {
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const ref = useRef<HTMLDivElement>(null);

  useLayoutEffect(() => {
    const el = ref.current;
    if (!el || expanded) return;
    // Not scrollHeight: a browser with the standard line-clamp treats the
    // lines under the fold as collapsed, not overflowing, and reports the
    // folded height from both. Lift the fold for one synchronous layout and
    // compare the natural height with the folded one instead.
    const measure = () => {
      const folded = el.clientHeight;
      const { display, webkitLineClamp } = el.style;
      el.style.display = "block";
      el.style.webkitLineClamp = "unset";
      const natural = el.scrollHeight;
      el.style.display = display;
      el.style.webkitLineClamp = webkitLineClamp;
      setOverflows(natural > folded + 1);
    };
    measure();
    // Width changes what fits; a window that grows can un-fold a paragraph.
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
  }, [expanded, children]);

  const folded = !expanded;
  return (
    <div className={`flex flex-col items-start gap-2 ${className}`}>
      <div
        ref={ref}
        className="min-w-0 self-stretch"
        style={
          folded
            ? { display: "-webkit-box", WebkitBoxOrient: "vertical", WebkitLineClamp: lines, overflow: "hidden" }
            : undefined
        }
      >
        {children}
      </div>
      {(overflows || expanded) && (
        <button
          type="button"
          className="text-[13px] font-bold text-muted hover:text-ink"
          onClick={() => setExpanded((e) => !e)}
          aria-expanded={expanded}
        >
          {expanded ? less : more}
        </button>
      )}
    </div>
  );
}
