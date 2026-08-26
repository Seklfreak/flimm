import Foundation

/// Builds a query string the way the web client's `qs()` does: `nil` and empty
/// values are dropped, booleans are sent only when true, and insertion order
/// is preserved so URLs are comparable in tests.
struct QueryBuilder {
    private(set) var items: [URLQueryItem] = []

    init(_ items: [URLQueryItem] = []) {
        self.items = items
    }

    mutating func add(_ name: String, _ value: String?) {
        guard let value, !value.isEmpty else { return }
        items.append(URLQueryItem(name: name, value: value))
    }

    mutating func add(_ name: String, _ value: Int?) {
        guard let value else { return }
        items.append(URLQueryItem(name: name, value: String(value)))
    }

    /// Flag-style: omitted unless true, matching `unfeeded=true` / `unseen=true`.
    mutating func flag(_ name: String, _ value: Bool?) {
        guard value == true else { return }
        items.append(URLQueryItem(name: name, value: "true"))
    }

    mutating func add<T: RawRepresentable>(_ name: String, _ value: T?) where T.RawValue == String {
        add(name, value?.rawValue)
    }

    mutating func page(_ page: Int?, size: Int?) {
        add("page", page)
        add("page_size", size)
    }

    mutating func append(contentsOf other: [URLQueryItem]) {
        items.append(contentsOf: other)
    }
}
