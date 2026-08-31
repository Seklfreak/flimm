import Foundation

/// A page of up-next videos that says whether it is the queue or a guess.
///
/// `suggestions` is set only once the context has run out, when the items are
/// similar videos rather than the rest of the list. Nothing may autoplay into
/// them and no panel may show them under the context's own name: a guess
/// presented as a queue is how "up next" stops meaning anything. A server
/// older than the field simply never sets it, which reads as a real queue —
/// the same thing it was before.
public struct UpNextPage: Codable, Sendable, Hashable {
    public let page: Page<VideoSummary>
    public let suggestions: Bool

    public var items: [VideoSummary] { page.items }
    public var hasMore: Bool { page.hasMore }

    public init(page: Page<VideoSummary>, suggestions: Bool = false) {
        self.page = page
        self.suggestions = suggestions
    }

    private enum CodingKeys: String, CodingKey {
        case suggestions
    }

    /// The flag rides on the same flat object as the page, so both are read
    /// from the one container.
    public init(from decoder: any Decoder) throws {
        page = try Page<VideoSummary>(from: decoder)
        let c = try decoder.container(keyedBy: CodingKeys.self)
        suggestions = try c.decodeIfPresent(Bool.self, forKey: .suggestions) ?? false
    }

    public func encode(to encoder: any Encoder) throws {
        try page.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        if suggestions { try c.encode(suggestions, forKey: .suggestions) }
    }
}
