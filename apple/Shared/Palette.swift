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
    /// appearances are spelled out — and they are the web client's `--c-bg`
    /// rather than black and white: pure black across a 65-inch panel is a
    /// hole with menus floating in it, and pure white is a lamp.
    static let background = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.063, green: 0.067, blue: 0.075, alpha: 1) // #101113
            : UIColor(red: 0.984, green: 0.980, blue: 0.969, alpha: 1) // #fbfaf7
    })

    /// The top of the page, a few percent lighter than the bottom.
    static let pageTop = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.106, green: 0.114, blue: 0.133, alpha: 1) // #1B1D22
            : UIColor(red: 1.0, green: 0.996, blue: 0.988, alpha: 1)
    })

    /// The light source in the top-left corner of a TV page. Weak in the
    /// dark, weaker still in the light, where a blue wash over an off-white
    /// page would read as a printing fault rather than a light.
    static let pageGlow = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.36, green: 0.55, blue: 1.00, alpha: 0.16)
            : UIColor(red: 0.18, green: 0.43, blue: 0.96, alpha: 0.05)
    })

    /// The bottom of the page.
    static let pageBottom = Color(uiColor: UIColor { traits in
        traits.userInterfaceStyle == .dark
            ? UIColor(red: 0.035, green: 0.039, blue: 0.047, alpha: 1) // #090A0C
            : UIColor(red: 0.949, green: 0.941, blue: 0.925, alpha: 1)
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

#if os(tvOS)
/// What a TV page is filled with.
///
/// Not flat black. A 65-inch panel showing one colour end to end reads as a
/// hole with menus floating in it — there is nothing for a row of cards to sit
/// *on*. A vertical fall from `pageTop` to `pageBottom` gives the screen a
/// horizon, and a wide, weak wash of the brand blue off the top-left corner
/// gives it a light source, so the whole thing looks lit rather than switched
/// off. Both are far too subtle to compete with artwork; that is the point.
struct TVPageBackground: View {
    var body: some View {
        LinearGradient(colors: [Palette.pageTop, Palette.pageBottom], startPoint: .top, endPoint: .bottom)
            .overlay(alignment: .topLeading) {
                RadialGradient(
                    colors: [Palette.pageGlow, Palette.pageGlow.opacity(0)],
                    center: .topLeading,
                    startRadius: 0,
                    endRadius: 1500
                )
            }
            .ignoresSafeArea()
    }
}
#endif
