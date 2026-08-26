import SwiftUI

/// Column definitions for the iPad grids.
///
/// The counts are not hard-coded: `.adaptive` fits as many columns of at least
/// `minimum` as the container actually offers, which is what makes a full-width
/// iPad three columns and the same app in a 2/3 Split View two — without the
/// screens knowing anything about window sizes. A compact width keeps the
/// phone's single column, so nothing about the iPhone layout changes.
enum Grids {
    static let spacing: CGFloat = 20

    /// Video cards: three across a full-width iPad, fewer as the window narrows.
    static let videos = [GridItem(.adaptive(minimum: 240, maximum: 400), spacing: spacing, alignment: .top)]

    /// Wider tiles for playlists and search results, which carry more text.
    static let tiles = [GridItem(.adaptive(minimum: 300, maximum: 480), spacing: spacing, alignment: .top)]
}
