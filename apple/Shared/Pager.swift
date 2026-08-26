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
    typealias Fetch = @MainActor (_ page: Int) async throws -> Page<Item>

    private(set) var items: [Item] = []
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

    init(fetch: @escaping Fetch) {
        self.fetch = fetch
    }

    func reload() async {
        isLoading = true
        defer { isLoading = false }
        do {
            let page = try await fetch(0)
            items = page.items
            total = page.total
            hasMore = page.hasMore
            nextPage = 1
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
            let page = try await fetch(nextPage)
            // Ids can repeat when the underlying list shifts between pages;
            // dropping the duplicates keeps SwiftUI's diffing sane.
            let known = Set(items.map(\.id))
            items.append(contentsOf: page.items.filter { !known.contains($0.id) })
            total = page.total
            hasMore = page.hasMore
            nextPage += 1
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
}
