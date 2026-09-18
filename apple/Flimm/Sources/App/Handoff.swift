import FlimmKit
import SwiftUI

/// Which screen is on top, for Handoff to offer.
///
/// The screens say what they are rather than ``NavigationModel`` deriving it: a
/// `NavigationPath` knows how deep it is and nothing whatever about what is in
/// it, so there is no reading the top of a stack back out. One object, because
/// only one screen is on top, and because the publisher asks a single question.
@MainActor
@Observable
final class HandoffPage {
    private(set) var page: Continuation.Page?
    /// The screen's own title, which is what the banner on the other device
    /// reads. Often empty for a moment: a channel is a spinner before it is a
    /// name.
    private(set) var title = ""

    func show(_ page: Continuation.Page, title: String) {
        self.page = page
        self.title = title
    }

    /// A title that arrived after its screen did. Ignored unless that screen is
    /// still the one on top — a channel loading behind a pushed playlist must
    /// not retitle the playlist.
    func retitle(_ page: Continuation.Page, title: String) {
        guard self.page == page else { return }
        self.title = title
    }

    /// Only clears if the departing screen is still the one showing: a push
    /// builds the new screen before the old one goes away.
    func hide(_ page: Continuation.Page) {
        guard self.page == page else { return }
        self.page = nil
        title = ""
    }
}

extension View {
    /// Offers this screen to the devices around it.
    ///
    /// Sits beside `Analytics.screen(…)` on the screens that have an address of
    /// their own; everything else — an editor, a sheet, Settings — is left out
    /// deliberately, being either half-finished work or a place nobody wants to
    /// arrive on a second device.
    /// `page` is optional because a screen does not always know yet what it is
    /// showing — the Feeds screen has no feed until the list has loaded.
    func handoffPage(_ page: Continuation.Page?, title: String) -> some View {
        modifier(HandoffPageModifier(page: page, title: title))
    }
}

private struct HandoffPageModifier: ViewModifier {
    @Environment(HandoffPage.self) private var current
    let page: Continuation.Page?
    let title: String

    func body(content: Content) -> some View {
        content
            // Claimed on appear even with no title yet: appearing is what makes
            // a screen the top one, and a claim with an empty title publishes
            // nothing until the name arrives.
            .onAppear { show() }
            .onChange(of: title) { _, new in
                guard let page else { return }
                current.retitle(page, title: new)
            }
            // The screen learning what it shows, or SwiftUI reusing one view
            // for a sibling route.
            .onChange(of: page) { old, _ in
                if let old { current.hide(old) }
                show()
            }
            .onDisappear {
                guard let page else { return }
                current.hide(page)
            }
    }

    private func show() {
        guard let page else { return }
        current.show(page, title: title)
    }
}
