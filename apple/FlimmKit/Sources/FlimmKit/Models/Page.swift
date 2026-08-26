import Foundation

/// The envelope every paged list endpoint returns. `page` is 0-based.
public struct Page<Item: Codable & Sendable & Hashable>: Codable, Sendable, Hashable {
    /// Server default; the maximum is 100.
    public static var defaultSize: Int { 30 }

    public let items: [Item]
    public let page: Int
    public let pageSize: Int
    public let total: Int

    /// True while more pages remain — the infinite-scroll condition.
    public var hasMore: Bool { (page + 1) * pageSize < total }

    public init(items: [Item], page: Int = 0, pageSize: Int = Page.defaultSize, total: Int = 0) {
        self.items = items
        self.page = page
        self.pageSize = pageSize
        self.total = total
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = try c.decode(.items, or: [])
        page = try c.decode(.page, or: 0)
        pageSize = try c.decode(.pageSize, or: Page.defaultSize)
        total = try c.decode(.total, or: 0)
    }
}
