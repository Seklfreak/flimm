import FlimmKit
import Foundation
import SwiftUI

/// Keeps the list a screen is showing alive across shell rebuilds.
///
/// On iPad the horizontal size class flips whenever the window is resized in
/// Split View, which swaps the whole shell (`TabView` ⇄ `NavigationSplitView`)
/// and destroys every screen's `@State`. Without this, each flip would restart
/// the `.task` that loads a feed and re-fetch pages the user already has — and
/// scroll them back to the top. A `Pager` is keyed by *what it queries*, so the
/// rebuilt screen picks the same one up instead of making a new one.
///
/// It is a cache, not a source of truth: pull-to-refresh still reloads it, and
/// a key that has fallen out simply loads again.
@MainActor
final class PagerStore {
    private var cache: [String: Any] = [:]
    private var order: [String] = []
    /// Enough for the screens a user moves between; the oldest is dropped.
    private let limit = 12

    /// The cached list for `key`, if there is one *and* it ever loaded.
    ///
    /// A pager whose first load was cancelled — the screen asked for a
    /// different list before this one answered, which a size-class flip or a
    /// feed arriving a moment after the screen does — holds no items and never
    /// will: nothing retries it, because every later pass finds it here and
    /// hands it straight back. That was a feed reading "All caught up" over
    /// four unwatched videos. A pager that loaded and *failed* is a different
    /// thing and is kept: it carries an error the screen offers a retry for.
    func existing<Item>(_ key: String) -> Pager<Item>? {
        guard let hit = cache[key] as? Pager<Item>, hit.hasLoaded else { return nil }
        touch(key)
        return hit
    }

    func insert<Item>(_ pager: Pager<Item>, for key: String) {
        cache[key] = pager
        touch(key)
        while order.count > limit, let oldest = order.first {
            order.removeFirst()
            cache[oldest] = nil
        }
    }

    /// Whether the store still holds this exact pager under `key`.
    ///
    /// A screen that kept its pager through a player presentation asks this on
    /// the way back: if the answer is no, something dropped the cache while
    /// the screen was covered and the list it is showing is stale.
    func holds<Item>(_ pager: Pager<Item>, forKey key: String) -> Bool {
        (cache[key] as? Pager<Item>) === pager
    }

    func removeAll() {
        cache.removeAll()
        order.removeAll()
    }

    private func touch(_ key: String) {
        order.removeAll { $0 == key }
        order.append(key)
    }
}

/// Reloading a cached list after the player closes over it.
///
/// Dismissing the full-screen player is not a context change: the screen
/// underneath never changed what it is showing, so its `.task(id:)` does not
/// rerun and its `.onAppear` does not fire. But a video finished or marked
/// seen in there drops every cached pager
/// (``AppModel/videoListStateChanged()``), and an "Unseen" list that keeps
/// showing what the viewer just watched is the bug that follows.
///
/// The player's own request going back to `nil` is exactly "the viewer came
/// back to this list"; `isStale` then decides whether anything actually
/// changed, so a dismissal that invalidated nothing reloads nothing and the
/// list does not jump under the viewer.
///
/// `settled` runs first and is awaited: the request goes nil the instant the
/// player is dismissed, while its last heartbeat is still on the wire. Judging
/// staleness before that lands is how a feed kept showing the position from
/// before the video was opened — and a pull-to-refresh a moment later could
/// race the same heartbeat.
extension View {
    func reloadsWhenPlayerCloses<Request: Equatable>(
        request: Request?,
        settled: @escaping () async -> Void,
        isStale: @escaping () -> Bool,
        reload: @escaping () async -> Void
    ) -> some View {
        onChange(of: request) { old, new in
            guard old != nil, new == nil else { return }
            Task {
                await settled()
                guard isStale() else { return }
                await reload()
            }
        }
    }
}
