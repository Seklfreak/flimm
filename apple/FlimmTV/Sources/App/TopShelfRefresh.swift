import FlimmKit
import TVServices

/// Keeps the Home screen's top shelf in step with the app.
///
/// The extension that draws the shelf cannot fetch anything (see
/// ``TopShelfSnapshot``), so this is the only thing that ever puts content
/// there: the app writes what it is already showing, and tells tvOS to reload.
@MainActor
enum TopShelfRefresh {
    /// Publishes a feed's videos to the shelf.
    ///
    /// Only ever called for the feed the app *opens* on. Someone browsing
    /// another feed for a minute has not changed what their Home screen should
    /// offer, and a shelf that followed the last screen looked at would be a
    /// different thing every time.
    static func publish(feed: Feed, videos: [VideoSummary], client: APIClient) async {
        guard !videos.isEmpty else { return }
        guard await TopShelfWriter.write(feedName: feed.name, videos: videos, client: client) != nil else {
            return
        }
        TVTopShelfContentProvider.topShelfContentDidChange()
    }

    /// Publishes the feed the app opens on, fetching it rather than waiting to
    /// be handed one.
    ///
    /// The Feeds screen publishes what it is already showing, which costs
    /// nothing — but it only runs when that screen runs, and only when it has
    /// something on it. Opening the app on another tab, or straight into a
    /// video, or onto a pinned feed with nothing unseen left in it, all left
    /// the Home screen empty for ever. This runs at launch, so the shelf is a
    /// property of having opened Flimm rather than of where you happened to go.
    ///
    /// A feed with nothing unseen falls back to everything in it: the point of
    /// the row is to offer something, and "you are all caught up" is better
    /// said by the feed itself than by a blank Home screen.
    static func publishLaunchFeed(app: AppModel) async {
        guard let feed = app.launchFeed else { return }
        let client = app.client
        var items = (try? await client.feedVideos(feed.id, view: feed.hideSeen ? .unseen : .all))?.items ?? []
        if items.isEmpty, feed.hideSeen {
            items = (try? await client.feedVideos(feed.id, view: .all))?.items ?? []
        }
        await publish(feed: feed, videos: items, client: client)
    }

    /// Empties the shelf. Signing out has to take the Home screen with it: the
    /// row sits outside the app, where a signed-out person can still see it.
    static func clear() {
        TopShelfStore.clear()
        TVTopShelfContentProvider.topShelfContentDidChange()
    }
}
