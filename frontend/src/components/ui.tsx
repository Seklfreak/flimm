import { useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { retryMediaUrl } from "@/lib/media";

export function Spinner({ label }: { label?: string }) {
  return (
    <div className="flex items-center gap-3 text-muted text-sm font-semibold" role="status">
      <span className="h-4 w-4 animate-spin rounded-full border-2 border-hair-2 border-t-accent" />
      {label}
    </div>
  );
}

export function Segmented<T extends string>({
  value,
  onChange,
  options,
  size = "md",
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
  size?: "md" | "sm";
}) {
  return (
    // Wrap rather than scroll: these are short labels in a form/header, and a
    // clipped, scrollbar-hidden row leaves options unreachable (no cue, no
    // affordance). Wrapping needs neither.
    <div className="flex flex-wrap gap-1.5" role="tablist">
      {options.map((o) => (
        <button
          key={o.value}
          role="tab"
          aria-selected={o.value === value}
          className={`seg ${o.value === value ? "on" : ""} ${size === "sm" ? "!px-2.5 !py-1.5 !text-[12px]" : ""}`}
          onClick={() => onChange(o.value)}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function Toggle({ on, onChange, label }: { on: boolean; onChange: (v: boolean) => void; label?: string }) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={on}
      aria-label={label}
      onClick={() => onChange(!on)}
      className={`relative h-[26px] w-[44px] flex-none rounded-full transition-colors ${on ? "bg-accent" : "bg-hair-2"}`}
    >
      <span
        className={`absolute top-[3px] h-5 w-5 rounded-full bg-white transition-all ${on ? "right-[3px]" : "left-[3px]"}`}
      />
    </button>
  );
}

// Circle avatar with a letter fallback while/if the image fails.
export function Avatar({ src, name, size = 40 }: { src?: string | null; name: string; size?: number }) {
  return (
    <span
      className="flex flex-none items-center justify-center overflow-hidden rounded-full bg-av font-extrabold text-muted"
      style={{ width: size, height: size, fontSize: size * 0.4 }}
    >
      {src ? <MediaImg src={src} alt="" className="h-full w-full object-cover" /> : name.slice(0, 1).toUpperCase()}
    </span>
  );
}

// <img> that refreshes the media cookie once and retries when the load fails.
export function MediaImg({ src, alt, className }: { src: string; alt: string; className?: string }) {
  const [url, setUrl] = useState(src);
  const [hidden, setHidden] = useState(false);
  useEffect(() => {
    setUrl(src);
    setHidden(false);
  }, [src]);
  if (hidden) return null;
  return (
    <img
      src={url}
      alt={alt}
      loading="lazy"
      decoding="async"
      className={className}
      onError={() => {
        void retryMediaUrl(src).then((next) => {
          if (next) setUrl(next);
          else setHidden(true);
        });
      }}
    />
  );
}

export function ProgressBar({ value, className = "" }: { value: number; className?: string }) {
  return (
    <div className={`h-1 overflow-hidden rounded-sm bg-white/50 ${className}`}>
      <div className="h-full rounded-sm bg-accent" style={{ width: `${Math.round(Math.min(1, Math.max(0, value)) * 100)}%` }} />
    </div>
  );
}

export function CheckIcon({ size = 13, stroke = 3 }: { size?: number; stroke?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={stroke}>
      <path d="M5 12l5 5 9-10" />
    </svg>
  );
}

export function CloseIcon({ size = 16 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path d="M6 6l12 12M18 6L6 18" />
    </svg>
  );
}

/** A plain chevron; `direction` is where it points. */
export function ChevronIcon({ size = 16, direction = "right" }: { size?: number; direction?: "left" | "right" | "up" | "down" }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2.5"
      style={
        direction === "left"
          ? { transform: "scaleX(-1)" }
          : direction === "down"
            ? { transform: "rotate(90deg)" }
            : direction === "up"
              ? { transform: "rotate(-90deg)" }
              : undefined
      }
    >
      <path d="M9 5l7 7-7 7" />
    </svg>
  );
}

export function SearchIcon({ size = 20 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="11" cy="11" r="7" />
      <path d="M20 20l-3.5-3.5" />
    </svg>
  );
}

export function PinIcon({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path d="M9 4h6l-1 6 3 3v2h-6v5l-1 2-1-2v-5H6v-2l3-3z" />
    </svg>
  );
}

/** A thumb, pointing up or down: the two halves of a vote count. */
export function ThumbIcon({ size = 14, down = false }: { size?: number; down?: boolean }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinejoin="round"
      style={down ? { transform: "rotate(180deg)" } : undefined}
      aria-hidden="true"
    >
      <path d="M7 22V10l5-8a2.5 2.5 0 0 1 2.4 3.2L13.5 9H19a2 2 0 0 1 2 2.4l-1.7 8A2 2 0 0 1 17.3 21H7z" />
      <path d="M7 10H4a1 1 0 0 0-1 1v10a1 1 0 0 0 1 1h3" />
    </svg>
  );
}

export function HeadphonesIcon({ size = 14 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5">
      <path d="M4 14v-2a8 8 0 0 1 16 0v2" />
      <rect x="2" y="13" width="5" height="8" rx="2" />
      <rect x="17" y="13" width="5" height="8" rx="2" />
    </svg>
  );
}

export function SearchBox({
  value,
  onChange,
  placeholder,
  className = "",
  autoFocus,
  onSubmit,
}: {
  value: string;
  onChange: (v: string) => void;
  placeholder: string;
  className?: string;
  autoFocus?: boolean;
  onSubmit?: () => void;
}) {
  return (
    <label className={`flex items-center gap-2.5 rounded-full bg-raised px-3.5 py-2 text-[13px] font-semibold text-muted-2 ${className}`}>
      <SearchIcon />
      <input
        type="search"
        value={value}
        autoFocus={autoFocus}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") onSubmit?.();
          if (e.key === "Escape") onChange("");
        }}
        placeholder={placeholder}
        className="w-full min-w-0 bg-transparent text-ink outline-none"
      />
    </label>
  );
}

// Fires onVisible when the sentinel scrolls into view (infinite lists).
export function InfiniteSentinel({ onVisible, enabled }: { onVisible: () => void; enabled: boolean }) {
  const ref = useRef<HTMLDivElement>(null);
  // `onVisible` is in the dependencies deliberately, and every call site
  // passes an inline arrow, so the observer is rebuilt on each render. That
  // rebuild is what *re-arms* it: an IntersectionObserver reports a change in
  // intersection, and a sentinel that was already in view when the last page
  // arrived never changes state — so a single long-lived observer fires once
  // and the list stops loading. Holding the callback in a ref to "avoid the
  // churn" looks tidier and breaks paging; there is a test for it.
  useEffect(() => {
    if (!enabled || !ref.current || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) onVisible();
    }, { rootMargin: "600px" });
    io.observe(ref.current);
    return () => io.disconnect();
  }, [enabled, onVisible]);
  return <div ref={ref} className="h-px" />;
}

/** A page-level wait: spinner over its label, centred with the same vertical
 *  rhythm as EmptyState so the two states swap without the layout jumping. */
export function LoadingState({ label }: { label: string }) {
  return (
    <div className="flex flex-col items-center gap-3 py-16 text-muted text-sm font-semibold" role="status">
      <span className="h-5 w-5 animate-spin rounded-full border-2 border-hair-2 border-t-accent" />
      {label}
    </div>
  );
}

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <div className="flex flex-col items-center gap-2 py-16 text-center">
      <p className="text-[16px] font-extrabold">{title}</p>
      {hint && <p className="meta max-w-sm">{hint}</p>}
      {action}
    </div>
  );
}

export function ErrorState({ message, retry }: { message: string; retry?: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 py-16 text-center">
      <p className="text-sm font-semibold text-danger">{message}</p>
      {retry && (
        <button className="btn" onClick={retry}>
          Retry
        </button>
      )}
    </div>
  );
}

// Locks body scroll while a modal/sheet is open.
function useScrollLock(on: boolean) {
  useLayoutEffect(() => {
    if (!on) return;
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.body.style.overflow = prev;
    };
  }, [on]);
}

// Centered modal on desktop; full-height sheet on narrow screens. Portaled to
// document.body so it escapes the layout's stacking context.
export function Modal({
  onClose,
  children,
  width = 920,
  label,
}: {
  onClose: () => void;
  children: ReactNode;
  width?: number;
  label: string;
}) {
  useScrollLock(true);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-[rgba(23,24,26,0.45)] md:items-center md:p-6" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={label}
        onMouseDown={(e) => e.stopPropagation()}
        className="flex h-[94dvh] w-full flex-col overflow-hidden rounded-t-[24px] bg-bg shadow-modal md:h-auto md:max-h-[min(700px,92vh)] md:rounded-[22px]"
        style={{ maxWidth: width }}
      >
        <div className="mx-auto mt-2 h-1 w-9 flex-none rounded-sm bg-av md:hidden" />
        {children}
      </div>
    </div>,
    document.body,
  );
}

// Bottom sheet (mobile feed picker etc.).
export function Sheet({ onClose, children, label }: { onClose: () => void; children: ReactNode; label: string }) {
  useScrollLock(true);
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-end justify-center bg-[rgba(23,24,26,0.35)]" onMouseDown={onClose}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={label}
        onMouseDown={(e) => e.stopPropagation()}
        className="flex max-h-[85dvh] w-full max-w-lg flex-col gap-3.5 overflow-y-auto rounded-t-[24px] bg-bg px-5 pb-10 pt-3"
      >
        <div className="mx-auto h-1 w-9 flex-none rounded-sm bg-av" />
        {children}
      </div>
    </div>,
    document.body,
  );
}

// Anchored popover: renders into body at the anchor's rect; closes on outside click / Esc.
export function Popover({
  anchor,
  onClose,
  children,
  align = "right",
  width = 260,
}: {
  anchor: HTMLElement | null;
  onClose: () => void;
  children: ReactNode;
  align?: "left" | "right";
  width?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState<{ top: number; left: number } | null>(null);

  useLayoutEffect(() => {
    if (!anchor) return;
    const r = anchor.getBoundingClientRect();
    const vw = window.innerWidth;
    let left = align === "right" ? r.right - width : r.left;
    left = Math.max(8, Math.min(left, vw - width - 8));
    setPos({ top: r.bottom + 6, left });
  }, [anchor, align, width]);

  useEffect(() => {
    const onDown = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node) && !anchor?.contains(e.target as Node)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    document.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [anchor, onClose]);

  if (!anchor || !pos) return null;
  return createPortal(
    <div ref={ref} className="fixed z-50" style={{ top: pos.top, left: pos.left, width }}>
      {children}
    </div>,
    document.body,
  );
}
