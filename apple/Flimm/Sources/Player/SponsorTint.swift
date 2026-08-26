import FlimmKit
import SwiftUI

extension SponsorRules {
    /// The colour a category is drawn in on the scrubber. The rules themselves
    /// live in FlimmKit; only the paint is a UI concern.
    static func tint(for category: String) -> Color {
        switch category {
        case "sponsor": Color.green
        case "selfpromo": Color.yellow
        case "interaction": Color.purple
        case "intro", "outro": Color.cyan
        case "music_offtopic": Color.orange
        default: Color.gray
        }
    }
}
