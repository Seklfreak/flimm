import FlimmKit
import SwiftUI

/// "Playing on the Apple TV" — the standing offer to take over.
///
/// It sits above the tab bar (and above the detail column on iPad) whenever a
/// screen of this account's is playing, and it is the only entry point the
/// companion has. That is deliberate: a remote nobody can find is a remote
/// nobody uses, and there is nothing to configure or pair — if a television is
/// playing, the bar is there.
struct RemoteBar: View {
    @Environment(RemoteControl.self) private var remote

    @State private var isOpen = false
    /// So the debug door below fires once rather than on every re-appearance.
    @State private var openedOnLaunch = false

    var body: some View {
        if let session = remote.current {
            bar(session)
                .sheet(isPresented: $isOpen) { RemoteScreen() }
                .onAppear(perform: openOnLaunchIfAsked)
        }
    }

    /// The companion only exists while something else is playing, and a
    /// simulator cannot tap the bar to open it — the sibling of
    /// `FLIMM_OPEN_TAB` and `FLIMM_PLAY_VIDEO`, for the same reason. A shipped
    /// app has no such door.
    private func openOnLaunchIfAsked() {
        #if DEBUG
        guard !openedOnLaunch, ProcessInfo.processInfo.environment["FLIMM_OPEN_REMOTE"] == "1" else { return }
        openedOnLaunch = true
        isOpen = true
        #endif
    }

    private func bar(_ session: RemoteSession) -> some View {
        VStack(spacing: 0) {
            Divider()
            progress(session)
            HStack(spacing: 12) {
                MediaImage(path: session.thumbUrl)
                    .frame(width: 56, height: 32)
                    .clipShape(RoundedRectangle(cornerRadius: 5, style: .continuous))
                VStack(alignment: .leading, spacing: 1) {
                    Text(session.title.isEmpty ? "Playing" : session.title)
                        .font(.footnote.weight(.semibold))
                        .lineLimit(1)
                    Text(subtitle(session))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
                // Pause without opening anything, which is the one thing worth
                // reaching for in a hurry.
                Button {
                    Task { await remote.togglePlayPause() }
                } label: {
                    Image(systemName: session.paused ? "play.fill" : "pause.fill")
                        .font(.title3)
                        .frame(width: 44, height: 44)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(session.paused ? "Resume on \(session.device)" : "Pause on \(session.device)")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            .contentShape(Rectangle())
            // The row opens the companion; the button inside it does not, which
            // is why the tap is a gesture here rather than a button around
            // everything.
            .onTapGesture { isOpen = true }
        }
        .background(.bar)
        .accessibilityElement(children: .contain)
    }

    /// A hairline of progress, run forward from the last thing the screen said
    /// rather than redrawn only when it speaks; see ``RemoteClock``.
    private func progress(_ session: RemoteSession) -> some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            GeometryReader { geometry in
                let fraction = RemoteClock.progress(
                    of: session, receivedAt: remote.receivedAt, now: context.date
                )
                Rectangle()
                    .fill(Palette.accent)
                    .frame(width: geometry.size.width * fraction)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .frame(height: 2)
        .background(Palette.raised)
        .accessibilityHidden(true)
    }

    private func subtitle(_ session: RemoteSession) -> String {
        let screen = session.device.isEmpty ? "another screen" : session.device
        guard !remote.isReachable else { return "On \(screen)" }
        // Saying so is better than a scrubber that has quietly stopped being
        // about anything.
        return "On \(screen) · not connected"
    }
}

extension View {
    /// Puts the remote bar under a screen, above whatever navigation chrome it
    /// already has.
    func remoteBar() -> some View {
        safeAreaInset(edge: .bottom, spacing: 0) { RemoteBar() }
    }
}
