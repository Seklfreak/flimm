import { formatCount, remainingUnseen } from "../lib/format";
import { useEffect, useState } from "react";
import { Link, NavLink, Outlet, useLocation, useNavigate, useParams } from "react-router";
import { EVERYTHING_ID, type Feed, type HistoryEntry, type PlaylistSummary } from "@/lib/api";
import { useConfig } from "@/lib/config";
import { pinnedFeed, useFeeds, useInProgress, usePinnedPlaylists, useRemoveHistoryEntry } from "@/lib/queries";
import { fmtDuration, plural } from "@/lib/format";
import { SearchIcon, Sheet } from "./ui";
import { Thumb, watchHref } from "./VideoCard";

// Sidebar (≥ md) with feeds + library nav, per the Main artboard; bottom tab
// bar Feeds · Channels · Playlists · History on narrow screens (Mobile board).
export function Layout() {
  const { pathname } = useLocation();
  const isWatch = pathname.startsWith("/watch/");
  return (
    <div className="flex min-h-full">
      <Sidebar />
      <main className={`min-w-0 flex-1 pb-[calc(84px+env(safe-area-inset-bottom))] md:pb-0 ${isWatch ? "" : ""}`}>
        <Outlet />
      </main>
      <MobileTabBar />
    </div>
  );
}

const NAV = [
  { to: "/channels", label: "Channels", icon: ChannelsIcon },
  { to: "/playlists", label: "Playlists", icon: PlaylistsIcon },
  { to: "/history", label: "History", icon: HistoryIcon },
  { to: "/search", label: "Search", icon: SearchIcon },
  { to: "/settings", label: "Settings", icon: SettingsIcon },
];

// Keep the sidebar short; the History page holds the full list.
const CONTINUE_LIMIT = 5;

function Sidebar() {
  const feeds = useFeeds();
  const pinnedPlaylists = usePinnedPlaylists();
  const inProgress = useInProgress();
  const removeEntry = useRemoveHistoryEntry();
  const config = useConfig();
  const { pathname } = useLocation();
  const params = useParams();
  const pinned = pinnedFeed(feeds.data);
  const activeFeedId = pathname.startsWith("/feeds/") ? params.id : pathname === "/" ? pinned?.id : undefined;
  const activePlaylistId = pathname.startsWith("/playlists/") ? params.id : undefined;
  const playlists = pinnedPlaylists.data ?? [];

  return (
    <aside className="sticky top-0 hidden h-dvh w-[264px] flex-none flex-col gap-[26px] overflow-y-auto border-r border-hair px-5 py-8 md:flex">
      <Link
        to="/"
        className="flex items-center gap-3 px-2.5 text-[28px] font-extrabold tracking-[-0.02em] text-ink no-underline hover:text-ink"
      >
        <LogoMark />
        {/* A deployment can call itself anything (APP_NAME); at this size a
            long one has to give way rather than push the mark off the rail. */}
        <span className="min-w-0 truncate">{config.app_name}</span>
      </Link>
      <div className="flex flex-col gap-1">
        <div className="sec flex items-center justify-between px-2.5 pb-1">
          <span>Feeds</span>
          <Link to="/feeds/new" className="text-[12px] font-bold normal-case tracking-normal no-underline">
            New
          </Link>
        </div>
        {(feeds.data ?? []).map((f) => (
          <FeedNavItem key={f.id} feed={f} active={f.id === activeFeedId} />
        ))}
      </div>
      {playlists.length > 0 && (
        <div className="flex flex-col gap-1">
          <div className="sec px-2.5 pb-1">
            <span>Playlists</span>
          </div>
          {playlists.map((p) => (
            <PlaylistNavItem key={p.id} playlist={p} active={p.id === activePlaylistId} />
          ))}
        </div>
      )}
      {(inProgress.data?.items ?? []).length > 0 && (
        <div className="flex flex-col gap-1">
          <div className="sec flex items-center justify-between px-2.5 pb-1">
            <span>Continue watching</span>
            <Link to="/history?filter=in_progress" className="text-[12px] font-bold normal-case tracking-normal no-underline">
              All
            </Link>
          </div>
          {(inProgress.data?.items ?? []).slice(0, CONTINUE_LIMIT).map((e) => (
            <ContinueItem key={e.id} entry={e} onRemove={() => removeEntry.mutate(e.id)} />
          ))}
        </div>
      )}
      <nav className="flex flex-col gap-0.5">
        {NAV.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `flex items-center gap-3 rounded-xl p-2.5 text-[15px] font-bold no-underline transition-colors ${isActive ? "bg-raised text-ink" : "text-muted hover:text-ink"}`
            }
          >
            <Icon />
            <span>{label}</span>
          </NavLink>
        ))}
      </nav>
    </aside>
  );
}

function FeedNavItem({ feed, active }: { feed: Feed; active: boolean }) {
  return (
    <Link
      to={`/feeds/${feed.id}`}
      className={`flex items-center justify-between rounded-[10px] px-2.5 py-[9px] text-[14px] font-bold text-ink no-underline hover:text-ink ${active ? "bg-raised" : "hover:bg-raised/60"}`}
    >
      <span className="truncate">{feed.name}</span>
      <UnseenBadge n={feed.unseen_count} />
    </Link>
  );
}

// One "Continue watching" row. The dismiss button is a sibling of the link,
// not nested inside it, so tapping it can never navigate.
function ContinueItem({ entry, onRemove }: { entry: HistoryEntry; onRemove: () => void }) {
  const v = entry.video;
  return (
    <div className="group relative flex items-center gap-2.5 rounded-[10px] px-2.5 py-[7px] hover:bg-raised/60">
      <Link to={watchHref(v)} className="flex min-w-0 flex-1 items-center gap-2.5 text-ink no-underline hover:text-ink">
        <span className="w-14 flex-none">
          <Thumb video={v} compact className="!rounded-md" />
        </span>
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-[13px] font-bold leading-tight">{v.title}</span>
          <span className="meta text-[11px]">{fmtDuration(v.position)} / {fmtDuration(v.duration)}</span>
        </span>
      </Link>
      <button
        className="flex-none text-muted-3 opacity-0 transition-opacity group-hover:opacity-100 focus-visible:opacity-100 hover:text-danger"
        aria-label={`Dismiss ${v.title}`}
        onClick={onRemove}
      >
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M6 6l12 12M18 6L6 18" /></svg>
      </button>
    </div>
  );
}

function PlaylistNavItem({ playlist, active }: { playlist: PlaylistSummary; active: boolean }) {
  return (
    <Link
      to={`/playlists/${playlist.id}`}
      className={`flex items-center justify-between rounded-[10px] px-2.5 py-[9px] text-[14px] font-bold text-ink no-underline hover:text-ink ${active ? "bg-raised" : "hover:bg-raised/60"}`}
    >
      <span className="truncate">{playlist.name}</span>
      {/* A music playlist carries no watch state (see docs/api.md "Music
          playlists") — seen_count comes back zeroed, so an "unseen" badge
          here would misleadingly count every track. */}
      {!playlist.music && <UnseenBadge n={remainingUnseen(playlist.video_count, playlist.seen_count)} />}
    </Link>
  );
}

export function UnseenBadge({ n }: { n: number }) {
  return n > 0 ? <span className="badge">{formatCount(n)}</span> : <span className="text-[12px] font-semibold text-muted-3">·</span>;
}

function MobileTabBar() {
  const { pathname } = useLocation();
  const feedsActive = pathname === "/" || pathname.startsWith("/feeds") || pathname.startsWith("/watch");
  const tabs = [
    { to: "/", label: "Feeds", icon: HomeIcon, active: feedsActive },
    { to: "/channels", label: "Channels", icon: ChannelsIcon, active: pathname.startsWith("/channels") },
    { to: "/playlists", label: "Playlists", icon: PlaylistsIcon, active: pathname.startsWith("/playlists") },
    { to: "/history", label: "History", icon: HistoryIcon, active: pathname.startsWith("/history") },
  ];
  return (
    <nav className="fixed inset-x-0 bottom-0 z-40 flex justify-around border-t border-hair bg-bg/95 px-5 pb-[max(14px,env(safe-area-inset-bottom))] pt-3.5 text-[11px] font-semibold text-muted-2 backdrop-blur md:hidden">
      {tabs.map(({ to, label, icon: Icon, active }) => (
        <Link key={to} to={to} className={`flex flex-col items-center gap-1 no-underline ${active ? "text-ink" : "text-muted-2"}`}>
          <Icon />
          <span>{label}</span>
        </Link>
      ))}
    </nav>
  );
}

// Page header used by every list screen: title + meta on the left, actions on
// the right. On mobile the title can open the feed picker (chevron) and a search
// icon sits in the header.
export function PageHeader({
  title,
  meta,
  actions,
  feedPicker,
  breadcrumb,
  below,
}: {
  title: string;
  meta?: React.ReactNode;
  actions?: React.ReactNode;
  feedPicker?: boolean;
  breadcrumb?: { to: string; label: string };
  below?: React.ReactNode;
}) {
  const [open, setOpen] = useState(false);
  return (
    <div className="flex flex-col gap-3 px-5 pt-[max(20px,env(safe-area-inset-top))] md:gap-[22px] md:px-10 md:pt-8">
      {breadcrumb && (
        <div className="flex items-center gap-2 text-[13px] font-semibold text-muted-2">
          <Link to={breadcrumb.to} className="text-muted-2 no-underline hover:text-ink">
            {breadcrumb.label}
          </Link>
          <span>/</span>
          <span className="truncate text-ink">{title}</span>
        </div>
      )}
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-3">
        <div className="flex min-w-0 items-baseline gap-3.5">
          {feedPicker ? (
            <button className="flex items-center gap-1.5 md:pointer-events-none" onClick={() => setOpen(true)} aria-haspopup="dialog">
              <span className="h1 truncate">{title}</span>
              <svg className="md:hidden" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5"><path d="M6 9l6 6 6-6" /></svg>
            </button>
          ) : (
            <span className="h1 truncate">{title}</span>
          )}
          {meta && <span className="meta hidden md:inline">{meta}</span>}
          {feedPicker && (
            // The narrow layout has no sidebar and its tab bar is full, so the
            // two things it would otherwise lose — search and settings — sit
            // here beside the title.
            <span className="ml-auto flex items-center gap-4 md:hidden">
              <Link to="/search" className="text-ink" aria-label="Search">
                <SearchIcon />
              </Link>
              <Link to="/settings" className="text-ink" aria-label="Settings">
                <SettingsIcon />
              </Link>
            </span>
          )}
        </div>
        {actions && <div className="flex min-w-0 max-w-full items-center gap-2 overflow-x-auto no-scrollbar">{actions}</div>}
      </div>
      {meta && <span className="meta -mt-1 md:hidden">{meta}</span>}
      {below}
      {open && <FeedPickerSheet onClose={() => setOpen(false)} />}
    </div>
  );
}

function FeedPickerSheet({ onClose }: { onClose: () => void }) {
  const feeds = useFeeds();
  const navigate = useNavigate();
  const params = useParams();
  const { pathname } = useLocation();
  const pinned = pinnedFeed(feeds.data);
  const activeId = pathname.startsWith("/feeds/") ? params.id : pinned?.id;
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);
  const list = feeds.data ?? [];
  const custom = list.filter((f) => f.id !== EVERYTHING_ID);
  const everything = list.find((f) => f.id === EVERYTHING_ID);
  const go = (id: string) => {
    onClose();
    navigate(`/feeds/${id}`);
  };
  const item = (f: Feed) => (
    <button
      key={f.id}
      onClick={() => go(f.id)}
      className={`flex items-center justify-between rounded-[10px] px-3 py-[13px] text-left text-[14px] font-bold ${f.id === activeId ? "bg-raised" : ""}`}
    >
      <span className="flex flex-col gap-px">
        <span>{f.name}</span>
        <span className="meta">{f.id === EVERYTHING_ID ? `all ${plural(f.channel_count, "channel")}` : plural(f.channel_count, "channel")}</span>
      </span>
      <UnseenBadge n={f.unseen_count} />
    </button>
  );
  return (
    <Sheet onClose={onClose} label="Feeds">
      <div className="flex items-center justify-between">
        <span className="text-[20px] font-extrabold tracking-[-0.02em]">Feeds</span>
        <Link to="/feeds/new" onClick={onClose} className="text-[13px] font-bold no-underline">
          New feed
        </Link>
      </div>
      <div className="flex flex-col gap-0.5">
        {custom.map(item)}
        {everything && (
          <>
            <div className="my-1.5 h-px bg-hair" />
            {item(everything)}
          </>
        )}
      </div>
    </Sheet>
  );
}

// The mark: a play triangle with a ghost trail behind it — "flimmern", the
// flicker of a screen. Same geometry as every other place it appears, which
// scripts/make-icons.py renders from (the app icons, the tvOS layers, the
// favicon); the viewBox is the glyph's own bounds, so the mark fills the box
// it is given instead of floating in a third of it.
function LogoMark() {
  return (
    <svg className="flex-none text-accent" width="30" height="29" viewBox="16.5 17.5 30 29" fill="currentColor" stroke="currentColor" strokeWidth="5" strokeLinejoin="round" aria-hidden="true">
      <path d="M27 20L44 32L27 44Z" transform="translate(-8 0)" opacity="0.38" />
      <path d="M27 20L44 32L27 44Z" />
    </svg>
  );
}

function HomeIcon() {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 10l8-6 8 6v10H4z" /></svg>;
}
function ChannelsIcon() {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><rect x="3" y="5" width="18" height="14" rx="3" /><path d="M10 9l5 3-5 3z" fill="currentColor" /></svg>;
}
function PlaylistsIcon() {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M4 6h12M4 12h12M4 18h8" /><path d="M19 14l3 2-3 2z" fill="currentColor" /></svg>;
}
function SettingsIcon() {
  return (
    <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
      <circle cx="12" cy="12" r="3.2" />
      <path d="M12 3.5v2M12 18.5v2M3.5 12h2M18.5 12h2M6 6l1.4 1.4M16.6 16.6L18 18M18 6l-1.4 1.4M7.4 16.6L6 18" />
    </svg>
  );
}

function HistoryIcon() {
  return <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><circle cx="12" cy="12" r="8" /><path d="M12 8v4l3 2" /></svg>;
}
