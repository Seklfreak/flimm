import { useState } from "react";
import type { Comment } from "@/lib/api";
import { formatCount, relativeDay } from "@/lib/format";

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
}

// The archived comments, under the video.
//
// Collapsed to start with: they are the longest thing on the page and the
// least often wanted, and opening the section is what loads the first page —
// a video nobody scrolls to the bottom of costs no request at all.
export function Comments({
  comments,
  total,
  isLoading,
  hasMore,
  isFetchingMore,
  fetchMore,
  open,
  onToggle,
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
        <div className="flex flex-col gap-4 px-4 pb-4">
          {isLoading && <span className="text-[13px] font-semibold text-muted-2">Loading comments…</span>}
          {!isLoading && comments.length === 0 && (
            <span className="text-[13px] font-semibold text-muted-2">
              No comments were archived with this video.
            </span>
          )}
          {comments.map((c) => (
            <CommentThread key={c.id} comment={c} />
          ))}
          {hasMore && (
            <button className="btn self-start" onClick={fetchMore} disabled={isFetchingMore}>
              {isFetchingMore ? "Loading…" : "More comments"}
            </button>
          )}
        </div>
      )}
    </div>
  );
}

// One comment and its replies. Replies are folded away behind their count:
// a long thread under every comment is what makes a comment section unusable,
// and the count is enough to decide whether to look.
function CommentThread({ comment }: { comment: Comment }) {
  const [showReplies, setShowReplies] = useState(false);
  const replies = comment.replies ?? [];

  return (
    <div className="flex flex-col gap-2">
      <CommentBody comment={comment} />
      {replies.length > 0 && (
        <div className="flex flex-col gap-3 pl-11">
          <button
            className="self-start text-[12px] font-bold text-accent"
            onClick={() => setShowReplies((s) => !s)}
            aria-expanded={showReplies}
          >
            {showReplies ? "Hide" : "Show"} {replies.length} {replies.length === 1 ? "reply" : "replies"}
          </button>
          {showReplies && replies.map((r) => <CommentBody key={r.id} comment={r} />)}
        </div>
      )}
    </div>
  );
}

function CommentBody({ comment }: { comment: Comment }) {
  return (
    <div className="flex gap-3">
      {/* An initial, not an avatar: the archive's avatar URL points at
          Google's CDN, and loading it would tell a third party which videos
          are being watched — the one thing archived comments otherwise
          avoid. */}
      <div
        aria-hidden
        className="mt-0.5 grid size-8 flex-none place-items-center rounded-full bg-raised text-[13px] font-extrabold text-muted-2"
      >
        {initial(comment.author)}
      </div>
      <div className="flex min-w-0 flex-col gap-1">
        <div className="flex flex-wrap items-center gap-2 text-[12px] font-bold text-muted-2">
          <span className={comment.from_uploader ? "rounded-full bg-accent px-2 py-0.5 text-white" : "text-ink-2"}>
            {comment.author}
          </span>
          <span>{commentWhen(comment)}</span>
          {comment.likes > 0 && <span>· {formatCount(comment.likes)} likes</span>}
          {comment.hearted && <span title="Hearted by the uploader">· ♥</span>}
        </div>
        <p className="whitespace-pre-wrap text-[14px] font-medium leading-[1.5] text-ink-2">{comment.text}</p>
      </div>
    </div>
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
