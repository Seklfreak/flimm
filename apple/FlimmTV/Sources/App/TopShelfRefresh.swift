import FlimmKit
import TVServices

/// Keeps the Home screen's top shelf in step with the app.
///
/// The extension that draws the shelf cannot fetch anything (see
/// ``TopShelfSnapshot``), so this is the only thing that ever puts content
/// there: the app writes what it is already showing, and tells tvOS to reload.
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

    /// Empties the shelf. Signing out has to take the Home screen with it: the
    /// row sits outside the app, where a signed-out person can still see it.
    static func clear() {
        TopShelfStore.clear()
        TVTopShelfContentProvider.topShelfContentDidChange()
    }
}
