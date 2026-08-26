import FlimmKit
import Foundation

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

    func existing<Item>(_ key: String) -> Pager<Item>? {
        guard let hit = cache[key] as? Pager<Item> else { return nil }
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

    func removeAll() {
        cache.removeAll()
        order.removeAll()
    }

    private func touch(_ key: String) {
        order.removeAll { $0 == key }
        order.append(key)
    }
}
