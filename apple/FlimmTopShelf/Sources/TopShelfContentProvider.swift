import FlimmKit
import TVServices

/// The Apple TV Home screen's top shelf: the row above the icons, shown while
/// Flimm is focused in the top row.
///
/// It is a reader and nothing else. tvOS loads this extension in its own
/// process, with a short time budget and no session of ours, so everything it
/// shows — the videos, their titles, their artwork — was written into the
/// shared App Group container by the app the last time someone opened it. See
/// ``TopShelfSnapshot``.
final class TopShelfContentProvider: TVTopShelfContentProvider {
    override func loadTopShelfContent(completionHandler: @escaping (TVTopShelfContent?) -> Void) {
        guard let snapshot = TopShelfStore.read(), !snapshot.entries.isEmpty else {
            // Nothing to show is not an error: nobody has signed in yet, or
            // the feed is empty. tvOS then falls back to the app's static top
            // shelf image, which is the right thing to see.
            completionHandler(nil)
            return
        }
        let collection = TVTopShelfItemCollection(items: snapshot.entries.map(item(for:)))
        collection.title = snapshot.feedName
        completionHandler(TVTopShelfSectionedContent(sections: [collection]))
    }

    private func item(for entry: TopShelfEntry) -> TVTopShelfSectionedItem {
        let item = TVTopShelfSectionedItem(identifier: entry.videoID)
        item.title = entry.title
        // 16:9, like every thumbnail the archive holds.
        item.imageShape = .hdtv
        if let image = TopShelfStore.imageURL(for: entry) {
            // A file URL in the shared container, because the process drawing
            // the shelf fetches artwork itself and carries none of our auth.
            item.setImageURL(image, for: .screenScale1x)
            item.setImageURL(image, for: .screenScale2x)
        }
        // The same bar a card shows: how far into this video the viewer got.
        item.playbackProgress = entry.progress
        // Both actions play. There is no video detail screen on the TV —
        // selecting a video anywhere in this app plays it — so offering
        // "display" as something else would be inventing a screen.
        let action = TVTopShelfAction(url: TopShelfLink.play(entry.videoID))
        item.displayAction = action
        item.playAction = action
        return item
    }
}
