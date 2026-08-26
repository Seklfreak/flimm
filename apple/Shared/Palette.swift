import FlimmKit
import SwiftUI

extension AppTheme {
    /// `nil` follows the system, which is what `preferredColorScheme` wants.
    var colorScheme: ColorScheme? {
        switch self {
        case .system: nil
        case .light: .light
        case .dark: .dark
        }
    }
}

/// The app's semantic colours.
///
/// System materials do the heavy lifting so light and dark both look native;
/// only the brand blue and the danger red are literal, and they carry the same
/// values as the web client's `--c-accent` / `--c-danger`.
enum Palette {
    /// Brand blue — links, the selected tab, the scrubber's played portion.
    static let accent = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.36, green: 0.55, blue: 1.00, alpha: 1)
            : UIColor(red: 0.18, green: 0.43, blue: 0.96, alpha: 1)
    })

    static let danger = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.88, green: 0.35, blue: 0.29, alpha: 1)
            : UIColor(red: 0.75, green: 0.23, blue: 0.17, alpha: 1)
    })

    // The layered `secondarySystemBackground` / `tertiarySystemFill` /
    // `separator` colours are iOS-only; tvOS gets the same roles expressed as
    // opacities over `primary`, which follows its light and dark appearances.
    #if os(tvOS)
    /// Page background. tvOS has no `systemBackground`, so the two
    /// appearances are spelled out.
    static let background = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark ? .black : .white
    })
    /// Cards, rows, sheets sitting on the page.
    static let raised = Color.primary.opacity(0.08)
    /// The grey a thumbnail occupies before it loads.
    static let placeholder = Color.primary.opacity(0.12)
    /// Hairline dividers.
    static let hairline = Color.primary.opacity(0.18)
    #else
    /// Page background.
    static let background = Color(uiColor: .systemBackground)
    /// Cards, rows, sheets sitting on the page.
    static let raised = Color(uiColor: .secondarySystemBackground)
    /// The grey a thumbnail occupies before it loads.
    static let placeholder = Color(uiColor: .tertiarySystemFill)
    /// Hairline dividers.
    static let hairline = Color(uiColor: .separator)
    #endif

    /// Overlay chrome on top of video — always dark, never theme-dependent.
    static let overlay = Color.black.opacity(0.55)
}

extension View {
    /// The small dark pill used on thumbnails (duration, "Resume · 2:31").
    func pillStyle() -> some View {
        font(.caption2.weight(.bold))
            .foregroundStyle(.white)
            .padding(.horizontal, 6)
            .padding(.vertical, 3)
            .background(Palette.overlay, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
    }
}
