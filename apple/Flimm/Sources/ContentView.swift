import FlimmKit
import SwiftUI

/// The root: setup → sign-in → the app.
///
/// `AuthSession` never drops to `signedOut` for a transient failure — only a
/// dead refresh token does that — so a flaky network keeps the user where they
/// are instead of throwing them back to the setup screen.
struct ContentView: View {
    @Environment(AuthSession.self) private var session
    @Environment(PushCoordinator.self) private var push
    @Environment(\.scenePhase) private var scenePhase

    @State private var app: AppModel?
    /// Watches for a video playing on another screen of this account's — the
    /// Apple TV — so the companion bar can offer to steer it. It holds one
    /// long-poll request open while the app is on screen, which is why it is
    /// stopped the moment it is not.
    @State private var remote: RemoteControl?
    @State private var player = PlayerCoordinator()
    /// Shared by both shells; see ``RootShell``.
    @State private var nav = NavigationModel()
    /// Per-device playback settings (video quality). Unlike ``Prefs`` these
    /// never leave the device, so they are not tied to the account and outlive
    /// a sign-out.
    @State private var playback = PlaybackSettings()
    /// Handoff: what this phone is doing, offered to the devices beside it.
    /// Owned here rather than by the player, because a screen being read is as
    /// continuable as a video being watched and only one of the two is a
    /// playback session.
    @State private var handoff = HandoffPublisher()
    /// Which screen is on top — the half of that offer the player does not
    /// answer. See ``HandoffPage``.
    @State private var handoffPage = HandoffPage()
    /// A continuation that arrived before there was an app to open it in: a
    /// handoff that launches the app lands before the session is restored.
    @State private var pendingContinuation: Continuation?
    /// The server a continuation came from, when it is not this phone's. The
    /// ids in it mean nothing here, and saying so beats four screens of "not
    /// found".
    @State private var otherServer: String?

    var body: some View {
        Group {
            switch session.state {
            case .loading:
                LoadingState(label: "Starting…")
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .background(Palette.background)
            case .needsServer:
                ServerSetupView()
            case .signedOut:
                SignInView()
            case .signedIn:
                if let app, let remote {
                    RootShell()
                        .environment(app)
                        .environment(remote)
                        .environment(player)
                        .environment(nav)
                        .environment(playback)
                        .environment(handoffPage)
                } else {
                    LoadingState()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .background(Palette.background)
                }
            }
        }
        .preferredColorScheme(app?.prefs.theme.colorScheme)
        .task(id: sessionKey) { syncAppModel() }
        // A tapped notification. Handled here rather than in the delegate
        // because opening anything needs the player and the navigation
        // model, and a tap that launched the app arrives before either.
        .onChange(of: push.pendingLink) { _, _ in openPendingLink() }
        // Another device of this account's, in this room, handing over what it
        // was doing. It may arrive at launch — before the session is restored —
        // so it is held rather than acted on, exactly like a tapped
        // notification.
        .onContinueUserActivity(Continuation.activityType) { activity in
            pendingContinuation = Continuation(userInfo: activity.userInfo ?? [:])
            openPendingContinuation()
        }
        .alert("From another server", isPresented: showingOtherServer, presenting: otherServer) { _ in
            Button("OK", role: .cancel) {}
        } message: { host in
            Text("That was handed over from \(host). This device is signed in to a different Flimm, which has none of it.")
        }
        // A backgrounded phone has nobody to show a scrubber to, and the poll
        // it holds open would be a connection kept alive for nothing. Coming
        // back after a while reloads everything; see AppModel.sceneReturned.
        .onChange(of: scenePhase) { _, phase in
            if phase == .active {
                remote?.start()
                Task { await app?.sceneReturned() }
            } else {
                remote?.stop()
                app?.sceneLeft()
            }
        }
    }

    /// Changes when the session or the server does, which is exactly when the
    /// `APIClient` behind `AppModel` has to be replaced.
    private var sessionKey: String {
        "\(session.state)|\(session.server?.baseURL.absoluteString ?? "")"
    }

    private func syncAppModel() {
        guard session.state == .signedIn, let client = session.client else {
            app = nil
            push.attach(client: nil)
            remote?.stop()
            remote = nil
            // A signed-out session has no client to report progress with, so
            // the player goes with it rather than playing on silently.
            player.configure(app: nil, playback: playback)
            // An activity outlives the app that published it: a signed-out
            // phone must not go on offering a library it can no longer open.
            handoff.stop()
            return
        }
        if app?.client !== client {
            app = AppModel(client: client)
            remote?.stop()
            remote = RemoteControl(client: client)
        }
        remote?.start()
        push.attach(client: client)
        player.configure(app: app, playback: playback)
        let session = self.session
        let player = self.player
        let page = self.handoffPage
        handoff.start { Self.continuation(session: session, player: player, page: page) }
        openPendingLink()
        openPendingContinuation()
        openDebugVideo()
    }

    /// What this phone is doing, as another device would have to open it.
    ///
    /// The player wins when there is one: a video being watched is what
    /// somebody picking up an iPad means to carry on with, and the screen
    /// underneath it is still there when they close it.
    private static func continuation(
        session: AuthSession,
        player: PlayerCoordinator,
        page: HandoffPage
    ) -> Continuation? {
        guard let server = session.server?.baseURL else { return nil }
        if let playing = player.model?.continuation { return playing }
        guard let current = page.page, !page.title.isEmpty else { return nil }
        return Continuation(server: server, destination: .page(current), title: page.title)
    }

    private var showingOtherServer: Binding<Bool> {
        Binding(get: { otherServer != nil }, set: { if !$0 { otherServer = nil } })
    }

    /// Opens what another device handed over, once there is an app to open it
    /// in. Playback starts where that device actually was rather than from the
    /// server's held position, which is up to a heartbeat behind it.
    private func openPendingContinuation() {
        guard app != nil, let continuation = pendingContinuation, let server = session.server?.baseURL else { return }
        pendingContinuation = nil
        guard continuation.matches(server: server) else {
            otherServer = continuation.server.host() ?? continuation.server.absoluteString
            return
        }
        switch continuation.destination {
        case .watch(let videoId, let position, let context):
            player.play(videoId, context: context, startAt: position > 0 ? position : nil)
        case .page(.feed(let id)):
            nav.select(feed: id)
        case .page(.channel(let id)):
            nav.openChannel(id)
        case .page(.playlist(let id)):
            nav.openPlaylist(id)
        case .page(.history):
            nav.select(.history)
        case .page(.stats):
            nav.openStats()
        }
    }

    /// Opens what a tapped notification asked for, once there is an app to
    /// open it in. A single new video plays in its feed, so *up next* is the
    /// rest of that feed; a digest goes to the feed itself.
    private func openPendingLink() {
        guard app != nil, let link = push.pendingLink else { return }
        push.pendingLink = nil
        switch link {
        case .video(let id, let feedID):
            player.play(id, context: PlaybackContext(source: .feed(feedID)))
        case .feed(let id):
            nav.select(feed: id)
        }
    }

    /// Opens a video straight from launch, so a screen that only exists during
    /// playback can be reached in a simulator without tapping through the app:
    ///
    ///     SIMCTL_CHILD_FLIMM_PLAY_VIDEO=<video id> xcrun simctl launch <device> dev.winktech.flimm
    ///
    /// `FLIMM_PLAY_FEED` / `FLIMM_PLAY_PLAYLIST` open it *in* that context,
    /// which is the only way to reach the states that depend on one — the end
    /// of a list most of all, where up next turns into suggestions.
    ///
    /// Debug builds only; the TV app has the same door (see `TVRootView`).
    private func openDebugVideo() {
        #if DEBUG
        let env = ProcessInfo.processInfo.environment
        guard let id = env["FLIMM_PLAY_VIDEO"], !id.isEmpty else { return }
        var context = PlaybackContext.none
        if let feed = env["FLIMM_PLAY_FEED"], !feed.isEmpty {
            context = PlaybackContext(source: .feed(feed))
        } else if let playlist = env["FLIMM_PLAY_PLAYLIST"], !playlist.isEmpty {
            context = PlaybackContext(source: .playlist(playlist))
        }
        player.play(id, context: context)
        #endif
    }
}

#Preview {
    ContentView()
        .environment(AuthSession(redirectURI: AppConfig.redirectURI))
}
