import FlimmKit
import SwiftUI

/// Picks the shell for the current width and owns the app-wide keyboard
/// shortcuts.
///
/// Both shells render the same ``NavigationModel``, so the switch here is a
/// change of container and nothing else: an iPad dragged between Split View
/// sizes crosses this boundary constantly, and anything a shell owned itself
/// would be lost every time it did.
struct RootShell: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass

    @FocusState private var rootFocused: Bool

    private var isWide: Bool { horizontalSizeClass == .regular }

    var body: some View {
        Group {
            if isWide {
                RootSplitView()
            } else {
                RootTabView()
            }
        }
        .overlay(alignment: .topLeading) { commands }
        // `/` focuses search, as in the web client. It is a key press rather
        // than a shortcut so that a focused text field keeps its own slashes:
        // typing moves focus off the shell, and this never fires.
        .focusable(isWide)
        .focusEffectDisabled()
        .focused($rootFocused)
        .onKeyPress(KeyEquivalent("/")) {
            nav.focusSearch()
            return .handled
        }
        .onAppear { rootFocused = isWide }
        .task { await app.loadIfNeeded() }
    }

    /// ⌘F and ⌘, — modifier shortcuts, so they work wherever focus happens to
    /// be. They are invisible controls because the two shells put their own
    /// affordances in different places.
    private var commands: some View {
        ZStack {
            Button("Search") { nav.focusSearch() }
                .keyboardShortcut("f", modifiers: .command)
            Button("Settings") { nav.openSettings() }
                .keyboardShortcut(",", modifiers: .command)
        }
        .frame(width: 1, height: 1)
        .opacity(0.01)
        .allowsHitTesting(false)
        .accessibilityHidden(true)
    }
}
