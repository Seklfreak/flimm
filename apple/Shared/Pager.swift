import FlimmKit
import Foundation
import Observation

/// Drives a `Page<Item>` list: first load, "load more" when the last row
/// appears, and pull-to-refresh.
///
/// Paging is the server's — `page` is 0-based and `hasMore` comes straight off
/// the envelope, so nothing here guesses when the list ends.
@MainActor
@Observable
final class Pager<Item: Codable & Sendable & Hashable & Identifiable> {
    /// A page request: the offset a caller would have asked for, and the
    /// cursor the last response handed back. Lists the server composes lazily
    /// (feed and channel videos) should send the cursor — it resumes exactly
    /// where the last page stopped, where an offset makes the server walk
    /// every page before it. Lists that are not composed lazily ignore it.
    typealias Fetch = @MainActor (_ page: Int, _ cursor: String?) async throws -> Page<Item>

    private(set) var items: [Item] = []
    /// The server's `total`, which is a **floor** while `hasMore` is true —
    /// video lists are composed lazily and stop just past the window they were
    /// asked for. Safe for capacity hints, not for showing a count.
    private(set) var total = 0
    private(set) var isLoading = false
    private(set) var isLoadingMore = false
    private(set) var hasMore = false
    private(set) var error: String?
    /// False until the first response lands, so an empty list is not mistaken
    /// for "nothing here" while it is still loading.
    private(set) var hasLoaded = false

    private let fetch: Fetch
    private var nextPage = 0
    private var nextCursor: String?

    init(fetch: @escaping Fetch) {
        self.fetch = fetch
    }

    /// For a list with no cursor of its own — most of them.
    convenience init(fetch: @escaping @MainActor (_ page: Int) async throws -> Page<Item>) {
        self.init(fetch: { page, _ in try await fetch(page) })
    }

    func reload() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let page = try await fetch(0, nil)
            items = page.items
            total = page.total
            hasMore = page.hasMore
            nextPage = 1
            nextCursor = page.nextCursor
            error = nil
        } catch is CancellationError {
            return
        } catch {
            self.error = AppModel.message(for: error)
        }
        hasLoaded = true
    }

    func loadMore() async {
        guard hasMore, !isLoading, !isLoadingMore else { return }
        isLoadingMore = true
        defer { isLoadingMore = false }
        do {
            let page = try await fetch(nextPage, nextCursor)
            // Ids can repeat when the underlying list shifts between pages;
            // dropping the duplicates keeps SwiftUI's diffing sane.
            let known = Set(items.map(\.id))
            items.append(contentsOf: page.items.filter { !known.contains($0.id) })
            total = page.total
            hasMore = page.hasMore
            nextPage += 1
            nextCursor = page.nextCursor
        } catch {
            // Silent: the sentinel reappears and tries again on the next scroll.
        }
    }

    /// Called from a row's `onAppear`; loads the next page near the end.
    func loadMoreIfNeeded(after item: Item) async {
        guard let index = items.firstIndex(of: item) else { return }
        guard index >= items.count - 5 else { return }
        await loadMore()
    }

    /// Local removal after a delete, without a round trip for the whole list.
    func remove(id: Item.ID) {
        items.removeAll { $0.id == id }
        total = max(0, total - 1)
    }

    /// Puts a removed item back at the index it had — undoing ``remove(id:)``,
    /// which is what an "Undo" on a dismissed video does.
    func reinsert(_ item: Item, at index: Int) {
        items.insert(item, at: min(max(index, 0), items.count))
        total += 1
    }

    /// Swaps one item in place by id, without a round trip for the whole
    /// list — a field changed server-side (a video was dismissed or put
    /// back) but the row itself is not being added or removed.
    func replace(_ item: Item) {
        guard let index = items.firstIndex(where: { $0.id == item.id }) else { return }
        items[index] = item
    }
}
