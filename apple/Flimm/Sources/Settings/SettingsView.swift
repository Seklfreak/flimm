import FlimmKit
import SwiftUI

/// Preferences live on the server (`GET /me`, `PATCH /me/prefs`), so every
/// change here follows the account to the web client and back.
struct SettingsView: View {
    @Environment(AppModel.self) private var app
    @Environment(AuthSession.self) private var session
    @Environment(PushCoordinator.self) private var push
    /// Quality is per device, so it comes from here rather than from ``Prefs``.
    @Environment(PlaybackSettings.self) private var playback

    @State private var confirmSignOut = false
    @State private var confirmChangeServer = false

    private var prefs: Prefs { app.prefs }

    var body: some View {
        Form {
            serverSection
            playbackSection
            sponsorSection
            dearrowSection
            qualitySection
            subtitleSection
            everythingSection
            appearanceSection
            librarySection
            aboutSection
        }
        .navigationTitle("Settings")
        .onAppear { Analytics.screen(.settings) }
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog(
            session.requiresSignIn ? "Sign out?" : "Disconnect from this server?",
            isPresented: $confirmSignOut,
            titleVisibility: .visible
        ) {
            Button(session.requiresSignIn ? "Sign out" : "Disconnect", role: .destructive) {
                // The device stops being this account's before the session
                // that could tell the server so is gone.
                Task {
                    await push.unregister()
                    await session.signOut()
                }
            }
        }
        .confirmationDialog("Change server?", isPresented: $confirmChangeServer, titleVisibility: .visible) {
            Button("Sign out and change server", role: .destructive) {
                Task {
                    await push.unregister()
                    await session.forgetServer()
                }
            }
        } message: {
            Text("You'll be signed out and asked for a new server address.")
        }
    }

    // MARK: - Sections

    private var serverSection: some View {
        Section("Server") {
            LabeledContent("Address") {
                Text(session.server?.baseURL.absoluteString ?? "—")
                    .font(.footnote.monospaced())
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                    .truncationMode(.middle)
            }
            LabeledContent("Version", value: session.server?.config.version ?? "—")
            Button("Change server") { confirmChangeServer = true }
        }
    }

    private var accountFooter: String {
        // Worth saying plainly: on this server anyone who can reach it is this
        // same user, and there is nothing to sign out of.
        guard session.requiresSignIn else {
            return "This server runs with authentication disabled: no sign-in, and everyone who can reach it shares this account."
        }
        guard let me = app.me else { return "" }
        return me.isAdmin ? "Administrator" : ""
    }

    private var playbackSection: some View {
        Section {
            Toggle("Autoplay next video", isOn: bind(\.autoplay) { PrefsPatch(autoplay: $0) })
            Picker("Playback speed", selection: bind(\.playbackSpeed) { PrefsPatch(playbackSpeed: $0) }) {
                ForEach(PlaybackSpeeds.all, id: \.self) { speed in
                    Text(Fmt.speed(speed)).tag(speed)
                }
            }
            Toggle("Even out the volume", isOn: bind(\.normalizeLoudness) { PrefsPatch(normalizeLoudness: $0) })
            Toggle("SponsorBlock", isOn: bind(\.skipSponsors) { PrefsPatch(skipSponsors: $0) })
        } header: {
            Text("Playback")
        } footer: {
            Text("""
            Evening out the volume measures each video once and turns the loud \
            ones down, so you stop reaching for the volume between channels; \
            nothing is ever turned up. SponsorBlock is the master switch: off, \
            and no segment is skipped, muted or offered — they are still \
            tinted on the scrubber.
            """)
        }
    }

    /// What each SponsorBlock category does. Shown only while the master
    /// switch is on: nine rows explaining themselves under a setting that
    /// turns all of them off would be nine rows of noise.
    @ViewBuilder
    private var sponsorSection: some View {
        if prefs.skipSponsors {
            Section {
                ForEach(SponsorSetting.categories, id: \.self) { category in
                    Picker(SponsorRules.label(category), selection: sponsorBinding(category)) {
                        Text("Skip").tag(SponsorSetting.skip)
                        Text("Ask").tag(SponsorSetting.ask)
                        Text("Off").tag(SponsorSetting.off)
                    }
                }
            } header: {
                Text("SponsorBlock categories")
            } footer: {
                Text("""
                Skip jumps the segment, Ask offers a button in the player, \
                Off leaves it alone.
                """)
            }
        }
    }

    /// Crowd-sourced titles and thumbnails, each on its own: a viewer may
    /// trust what people wrote and not the frames they picked.
    private var dearrowSection: some View {
        Section {
            Picker("Titles", selection: bind(\.dearrowTitles) { PrefsPatch(dearrowTitles: $0) }) {
                ForEach(DeArrowSetting.allCases, id: \.self) { Text($0.label).tag($0) }
            }
            Picker("Thumbnails", selection: bind(\.dearrowThumbnails) { PrefsPatch(dearrowThumbnails: $0) }) {
                ForEach(DeArrowSetting.allCases, id: \.self) { Text($0.label).tag($0) }
            }
        } header: {
            Text("DeArrow")
        } footer: {
            Text("""
            Crowd-sourced titles and thumbnails. Manual uses what people \
            submitted and voted on; All also tidies a shouted title and picks \
            a frame where nobody has. The frame is cut from your own copy of \
            the video — DeArrow supplies a timestamp, never an image.
            """)
        }
    }

    /// A category's setting, written back as the whole map: the server
    /// replaces what it is sent, so a patch carrying one category would drop
    /// the rest to their defaults.
    private func sponsorBinding(_ category: String) -> Binding<SponsorSetting> {
        Binding(
            get: { prefs.sponsorActions[category] ?? .off },
            set: { value in
                var next = prefs.sponsorActions
                next[category] = value
                Task { await app.updatePrefs(PrefsPatch(sponsorActions: next)) }
            }
        )
    }

    /// The one playback setting that does not follow the account: quality is
    /// about this screen and this network.
    private var qualitySection: some View {
        Section {
            Picker("Video quality", selection: qualityBinding) {
                ForEach(VideoQuality.options) { option in
                    Text(VideoQuality.label(option)).tag(option)
                }
            }
        } header: {
            Text("Video quality")
        } footer: {
            Text("""
            Auto plays the archived file whenever this device can decode it — \
            full quality, and nothing for the server to convert — and otherwise \
            the tallest rendition this screen can show. A fixed height always \
            plays a converted rendition, even when the archive would have \
            played, and falls to the nearest lower one a video offers. This \
            setting stays on this device.
            """)
        }
    }

    private var subtitleSection: some View {
        Section("Subtitles") {
            Picker("Language", selection: bind(\.subtitleLang) { PrefsPatch(subtitleLang: $0) }) {
                Text("Off").tag(Prefs.subtitlesOff)
                ForEach(SubtitleLanguages.common, id: \.self) { code in
                    Text(Fmt.langName(code)).tag(code)
                }
                if !SubtitleLanguages.common.contains(prefs.subtitleLang), prefs.subtitleLang != Prefs.subtitlesOff {
                    Text(Fmt.langName(prefs.subtitleLang)).tag(prefs.subtitleLang)
                }
            }
            Picker("Size", selection: bind(\.subtitleSize) { PrefsPatch(subtitleSize: $0) }) {
                Text("Small").tag(SubtitleSize.small)
                Text("Medium").tag(SubtitleSize.medium)
                Text("Large").tag(SubtitleSize.large)
            }
        }
    }

    private var everythingSection: some View {
        Section {
            Picker("Sort", selection: bind(\.everythingSort) { PrefsPatch(everythingSort: $0) }) {
                ForEach(FeedSort.allCases, id: \.self) { option in
                    Text(option.label).tag(option)
                }
            }
            Toggle("Hide seen", isOn: bind(\.everythingHideSeen) { PrefsPatch(everythingHideSeen: $0) })
            Toggle("Include Shorts", isOn: bind(\.everythingIncludeShorts) { PrefsPatch(everythingIncludeShorts: $0) })
        } header: {
            Text("“Everything” feed")
        } footer: {
            Text("The built-in feed over every channel. Its options are preferences, not feed settings.")
        }
    }

    private var appearanceSection: some View {
        Section("Appearance") {
            Picker("Theme", selection: bind(\.theme) { PrefsPatch(theme: $0) }) {
                Text("System").tag(AppTheme.system)
                Text("Light").tag(AppTheme.light)
                Text("Dark").tag(AppTheme.dark)
            }
        }
    }

    private var librarySection: some View {
        Section("Library") {
            NavigationLink("Manage feeds", value: Route.feedManager)
        }
    }

    private var aboutSection: some View {
        Section {
            LabeledContent("Signed in as", value: app.me?.name.isEmpty == false ? (app.me?.name ?? "") : (app.me?.email ?? "—"))
            if let email = app.me?.email, !email.isEmpty, app.me?.name.isEmpty == false {
                LabeledContent("Email", value: email)
            }
            LabeledContent("App version", value: AppConfig.displayVersion)
            Button(session.requiresSignIn ? "Sign out" : "Disconnect", role: .destructive) {
                confirmSignOut = true
            }
        } header: {
            Text("Account")
        } footer: {
            Text(accountFooter)
        }
    }

    // MARK: - Binding helpers

    private var qualityBinding: Binding<QualityPreference> {
        Binding(
            get: { playback.videoQuality },
            set: { playback.videoQuality = $0 }
        )
    }

    /// Reads a pref locally and writes it through `PATCH /me/prefs`.
    private func bind<Value>(
        _ keyPath: KeyPath<Prefs, Value>,
        patch: @escaping (Value) -> PrefsPatch
    ) -> Binding<Value> {
        Binding(
            get: { app.prefs[keyPath: keyPath] },
            set: { value in Task { await app.updatePrefs(patch(value)) } }
        )
    }
}
