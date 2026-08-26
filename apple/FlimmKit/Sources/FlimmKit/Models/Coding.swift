import Foundation

/// JSON coders configured for the Flimm API contract (`docs/api.md`): the wire
/// format is snake_case and times are RFC 3339 UTC.
public enum FlimmCoding {
    /// Decoder for every `/api/v1` response.
    public static var decoder: JSONDecoder {
        let decoder = JSONDecoder()
        decoder.keyDecodingStrategy = .convertFromSnakeCase
        decoder.dateDecodingStrategy = .custom { decoder in
            let raw = try decoder.singleValueContainer().decode(String.self)
            guard let date = RFC3339.date(from: raw) else {
                throw DecodingError.dataCorrupted(
                    .init(codingPath: decoder.codingPath, debugDescription: "not an RFC 3339 timestamp: \(raw)")
                )
            }
            return date
        }
        return decoder
    }

    /// Encoder for request bodies.
    public static var encoder: JSONEncoder {
        let encoder = JSONEncoder()
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var container = encoder.singleValueContainer()
            try container.encode(RFC3339.string(from: date))
        }
        return encoder
    }
}

/// RFC 3339 parsing that tolerates both shapes the backend emits.
///
/// Go's `time.Time` marshals as RFC 3339 *Nano*: a timestamp with a whole
/// number of seconds loses its fractional part entirely, while anything else
/// keeps it. A single `ISO8601DateFormatter` handles exactly one of the two,
/// so both are tried — getting this wrong makes `published` decode fine in
/// tests and fail against a live server.
enum RFC3339 {
    /// `ISO8601DateFormatter` is not `Sendable` and is expensive to build, so
    /// one pair is shared behind a lock rather than allocated per value.
    private final class Formatters: @unchecked Sendable {
        private let lock = NSLock()
        private let withFraction: ISO8601DateFormatter
        private let plain: ISO8601DateFormatter

        init() {
            withFraction = ISO8601DateFormatter()
            withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            plain = ISO8601DateFormatter()
            plain.formatOptions = [.withInternetDateTime]
        }

        func date(from string: String) -> Date? {
            lock.withLock { withFraction.date(from: string) ?? plain.date(from: string) }
        }

        func string(from date: Date) -> String {
            lock.withLock { plain.string(from: date) }
        }
    }

    private static let formatters = Formatters()

    static func date(from string: String) -> Date? {
        formatters.date(from: string)
    }

    static func string(from date: Date) -> String {
        formatters.string(from: date)
    }
}

extension KeyedDecodingContainer {
    /// Decode a key that the server may legitimately omit — a music playlist's
    /// suppressed watch state, a field added after a client shipped — without
    /// failing the whole response.
    func decode<T: Decodable>(_ key: Key, or fallback: T) throws -> T {
        try decodeIfPresent(T.self, forKey: key) ?? fallback
    }
}
