import Foundation

/// The URLs the top shelf hands back to the app.
///
/// A top-shelf action is a URL that tvOS opens in the app; there is no other
/// channel between the two. The extension builds them and the app reads them,
/// which is why the shape lives here rather than in either.
public enum TopShelfLink {
    /// `dev.winktech.flimm.tv://play/<video id>`
    public static func play(_ videoID: String) -> URL {
        var components = URLComponents()
        components.scheme = TopShelfStore.urlScheme
        components.host = "play"
        components.path = "/" + videoID
        // The scheme and host are literals and an id is path-safe, so this
        // cannot fail — but a crash on the Home screen would be a poor way to
        // find out otherwise.
        return components.url ?? URL(string: "\(TopShelfStore.urlScheme)://play")!
    }

    /// The video id in a link the app was opened with, or nil when the URL is
    /// something else entirely.
    public static func videoID(from url: URL) -> String? {
        guard url.scheme == TopShelfStore.urlScheme, url.host == "play" else { return nil }
        let id = url.path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        return id.isEmpty ? nil : id
    }
}
