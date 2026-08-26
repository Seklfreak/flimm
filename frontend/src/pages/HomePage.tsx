import { Navigate } from "react-router";
import { pinnedFeed, useFeeds } from "@/lib/queries";
import { ErrorState, Spinner } from "@/components/ui";

// "/" opens the pinned feed (or the first custom feed, or Everything).
export default function HomePage() {
  const feeds = useFeeds();
  if (feeds.isLoading) return <div className="p-10"><Spinner label="Loading feeds…" /></div>;
  if (feeds.isError) return <ErrorState message={feeds.error.message} retry={() => feeds.refetch()} />;
  const feed = pinnedFeed(feeds.data);
  if (!feed) return <Navigate to="/feeds/new" replace />;
  return <Navigate to={`/feeds/${feed.id}`} replace />;
}
