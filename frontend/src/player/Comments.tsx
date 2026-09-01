import { useState } from "react";
import { ThumbIcon } from "@/components/ui";
import type { Comment } from "@/lib/api";
import { formatCount, relativeDay } from "@/lib/format";
import { Clamp } from "./Clamp";
import { RichText } from "./RichText";

export interface CommentsProps {
  comments: Comment[];
  total: number;
  isLoading: boolean;
  hasMore: boolean;
  isFetchingMore: boolean;
  fetchMore: () => void;
  /** Controlled by the page, because opening the section is what starts the
   *  request — the query is enabled on exactly this. */
  open: boolean;
  onToggle: (open: boolean) => void;
  /** The video's length: a timestamp in a comment seeks, unless it points
   *  past the end. */
  duration: number;
  onSeek: (seconds: number) => void;
}

// The archived comments, under the video's description.
//
// Open to start with: what people said about a video belongs under it, not
// behind a control that has to be found first. Closing the section is
// remembered for the session (see WatchPage), so someone who does not want
// comments closes them once rather than on every video — and while it is
// closed nothing is requested, which is what the `open` prop is for.
export function Comments({
  comments,
  total,
  isLoading,
  hasMore,
  isFetchingMore,
  fetchMore,
  open,
  onToggle,
  duration,
  onSeek,
}: CommentsProps) {
  return (
    <div className="rounded-[14px] bg-raised-2">
      <button
        className="flex w-full items-center justify-between gap-3 px-4 py-3 text-left"
        onClick={() => onToggle(!open)}
        aria-expanded={open}
      >
        <span className="text-[14px] font-extrabold">
          Comments{total > 0 ? ` · ${formatCount(total)}` : ""}
        </span>
        <svg
          width="16"
          height="16"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          strokeWidth="2"
          className={`flex-none text-muted-2 transition-transform ${open ? "rotate-180" : ""}`}
        >
          <path d="M6 9l6 6 6-6" />
        </svg>
      </button>
      {open && (
        <div className="flex flex-col px-4 pb-4">
          {isLoading && <span className="pb-1 text-[13px] font-semibold text-muted-2">Loading comments…</span>}
          {!isLoading && comments.length === 0 && (
            <span className="pb-1 text-[13px] font-semibold text-muted-2">
              No comments were archived with this video.
            </span>
          )}
          {/* A hairline between threads, not a gap: what separates two
              comments has to be visible, or a short one reads as the
              previous one's last line. */}
          <div className="flex flex-col divide-y divide-hair-2">
            {comments.map((c) => (
              <CommentThread key={c.id} comment={c} duration={duration} onSeek={onSeek} />
            ))}
          </div>
          {hasMore && (
            <button className="btn mt-4 self-start" onClick={fetchMore} disabled={isFetchingMore}>
              {isFetchingMore ? "Loading…" : "More comments"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

interface ThreadProps {
  comment: Comment;
  duration: number;
  onSeek: (seconds: number) => void;
}

// One comment and its replies. Replies are folded away behind their count:
// a long thread under every comment is what makes a comment section unusable,
// and the count is enough to decide whether to look.
function CommentThread({ comment, duration, onSeek }: ThreadProps) {
  const [showReplies, setShowReplies] = useState(false);
  const replies = comment.replies ?? [];

  return (
    <div className="flex flex-col gap-3 py-4 first:pt-1 last:pb-1">
      <CommentBody comment={comment} duration={duration} onSeek={onSeek} />
      {replies.length > 0 && (
        // The rail is what says these belong to the comment above, the way a
        // thread reads anywhere else; indentation alone did not.
        <div className="ml-[18px] flex flex-col gap-4 border-l-2 border-hair-2 pl-[26px]">
          <button
            className="flex items-center gap-1.5 self-start text-[13px] font-bold text-accent hover:text-accent-deep"
            onClick={() => setShowReplies((s) => !s)}
            aria-expanded={showReplies}
          >
            <svg
              width="14"
              height="14"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="2.5"
              className={`transition-transform ${showReplies ? "rotate-180" : ""}`}
            >
              <path d="M6 9l6 6 6-6" />
            </svg>
            {showReplies ? "Hide" : "Show"} {replies.length} {replies.length === 1 ? "reply" : "replies"}
          </button>
          {showReplies && replies.map((r) => <CommentBody key={r.id} comment={r} duration={duration} onSeek={onSeek} reply />)}
        </div>
      )}
    </div>
  );
}

function CommentBody({ comment, duration, onSeek, reply = false }: ThreadProps & { reply?: boolean }) {
  return (
    <div className="flex gap-3">
      {/* An initial, not an avatar: the archive's avatar URL points at
          Google's CDN, and loading it would tell a third party which videos
          are being watched — the one thing archived comments otherwise
          avoid. */}
      <div
        aria-hidden
        className={`mt-0.5 grid flex-none place-items-center rounded-full font-extrabold ${
          reply ? "size-7 bg-hair-2 text-[11px] text-muted" : "size-9 bg-hair-2 text-[13px] text-ink-2"
        }`}
      >
        {initial(comment.author)}
      </div>
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px] font-semibold text-muted">
          <span
            className={
              comment.from_uploader
                ? "rounded-full bg-accent px-2 py-0.5 text-[12px] font-bold text-white"
                : "text-[13px] font-bold text-ink"
            }
          >
            {comment.author}
          </span>
          <span>{commentWhen(comment)}</span>
          {comment.likes > 0 && (
            <span className="flex items-center gap-1">
              <ThumbIcon size={12} />
              {formatCount(comment.likes)}
            </span>
          )}
          {comment.hearted && (
            <span className="flex items-center gap-1 text-[#e0245e]" title="Hearted by the uploader">
              <HeartIcon />
              <span className="sr-only">Hearted by the uploader</span>
            </span>
          )}
        </div>
        {/* A long comment folds like the description does, so one essay
            does not push every other comment off the screen. */}
        <Clamp lines={6} more="Read more" less="Show less">
          <p className="whitespace-pre-wrap text-[15px] leading-[1.55] text-ink [overflow-wrap:anywhere]">
            <RichText text={comment.text} duration={duration} onSeek={onSeek} />
          </p>
        </Clamp>
      </div>
    </div>
  );
}

function HeartIcon() {
  return (
    <svg width="13" height="13" viewBox="0 0 24 24" fill="currentColor" aria-hidden>
      <path d="M12 21s-7.5-4.6-9.6-9.2C.9 8.3 3 4.5 6.8 4.5c2 0 3.6 1 4.6 2.5A5.5 5.5 0 0 1 16 4.5c3.8 0 6 3.8 4.4 7.3C19.5 16.4 12 21 12 21z" />
    </svg>
  );
}

/** The date when the archive kept one, else upstream's own wording. */
export function commentWhen(comment: Comment, now: Date = new Date()): string {
  if (comment.published) return relativeDay(comment.published, now);
  return comment.time_text ?? "";
}

export function initial(author: string): string {
  // Authors come through as "@someone"; the @ is not an initial.
  const letters = author.replace(/^@+/, "").trim();
  return letters ? letters[0]!.toUpperCase() : "?";
}
