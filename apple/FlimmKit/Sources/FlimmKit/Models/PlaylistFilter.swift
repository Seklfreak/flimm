import Foundation

/// Filters a playlist's items in place, on the client.
///
/// `GET /playlists/{id}` already carries every item, and TubeArchivist's
/// search returns at most 30 hits across the whole archive, so scoping a
/// search to one playlist would miss most of a long one. Every word of the
/// query must appear in the title or the channel name, in any order, ignoring
/// case and accents: "winter daughter" finds Daughter's "Winter", "beyonce"
/// finds "Beyoncé". The web's `filterPlaylistItems` is the same rule.
public enum PlaylistFilter {
    public static func filter(_ items: [PlaylistItem], query: String) -> [PlaylistItem] {
        let words = fold(query).split(whereSeparator: \.isWhitespace)
        guard !words.isEmpty else { return items }
        return items.filter { item in
            let haystack = fold("\(item.video.title) \(item.video.channel.name)")
            return words.allSatisfy { haystack.contains($0) }
        }
    }

    private static func fold(_ text: String) -> String {
        text.folding(options: [.caseInsensitive, .diacriticInsensitive], locale: nil)
    }
}
