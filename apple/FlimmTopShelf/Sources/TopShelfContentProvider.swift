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
            // Nothing written yet: nobody has signed in, or the app has not
            // been opened since it was installed. `nil` hands the shelf back
            // to tvOS, which shows the app's own top shelf image — the right
            // thing to look at, and better than a card explaining itself.
            // What went wrong, when something has, is Settings' job.
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
        if let raw = entry.imageURL, let image = URL(string: raw) {
            // A URL the *system* fetches, carrying its own media token: tvOS
            // draws this row in a process with no session of ours, and an App
            // Group on tvOS is shared preferences rather than a writable
            // directory, so there is nowhere to leave a file for it.
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
