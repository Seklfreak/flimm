import Foundation

/// The envelope every paged list endpoint returns. `page` is 0-based.
public struct Page<Item: Codable & Sendable & Hashable>: Codable, Sendable, Hashable {
    /// Server default; the maximum is 100.
    public static var defaultSize: Int { 30 }

    public let items: [Item]
    public let page: Int
    public let pageSize: Int
    /// Exact only when there is nothing more to come: the server composes
    /// lists lazily and stops one item past the window it was asked for, so
    /// while more remains this is a floor, not the length of the list.
    public let total: Int

    /// The server's own answer to "is there another page". Absent on a server
    /// older than the field, hence optional.
    private let moreRemaining: Bool?

    /// Resumes exactly here on the next request. Following it keeps a deep
    /// page as cheap as the first; asking for the offset instead makes the
    /// server walk every page before it. Absent when there is nothing more.
    public let nextCursor: String?

    /// True while more pages remain — the infinite-scroll condition.
    ///
    /// The server's flag wins where it exists. Measuring the offset against
    /// `total` only holds for a list counted in full, and would end a lazily
    /// composed list after its first page.
    public var hasMore: Bool { moreRemaining ?? ((page + 1) * pageSize < total) }

    public init(
        items: [Item],
        page: Int = 0,
        pageSize: Int = Page.defaultSize,
        total: Int = 0,
        hasMore: Bool? = nil,
        nextCursor: String? = nil
    ) {
        self.items = items
        self.page = page
        self.pageSize = pageSize
        self.total = total
        self.moreRemaining = hasMore
        self.nextCursor = nextCursor
    }

    /// `hasMore` carries the wire name; the stored property is called
    /// something else so the computed one above can keep the good name.
    private enum CodingKeys: String, CodingKey {
        case items, page, pageSize, total, hasMore, nextCursor
    }

    public init(from decoder: any Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        items = try c.decode(.items, or: [])
        page = try c.decode(.page, or: 0)
        pageSize = try c.decode(.pageSize, or: Page.defaultSize)
        total = try c.decode(.total, or: 0)
        moreRemaining = try c.decodeIfPresent(Bool.self, forKey: .hasMore)
        nextCursor = try c.decodeIfPresent(String.self, forKey: .nextCursor)
    }

    public func encode(to encoder: any Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(items, forKey: .items)
        try c.encode(page, forKey: .page)
        try c.encode(pageSize, forKey: .pageSize)
        try c.encode(total, forKey: .total)
        try c.encodeIfPresent(moreRemaining, forKey: .hasMore)
        try c.encodeIfPresent(nextCursor, forKey: .nextCursor)
    }
}
