import FlimmKit
import SwiftUI

/// Preferences live on the server (`GET /me`, `PATCH /me/prefs`), so every
/// change here follows the account to the web client and back.
struct SettingsView: View {
    @Environment(AppModel.self) private var app
    @Environment(AuthSession.self) private var session

    @State private var confirmSignOut = false
    @State private var confirmChangeServer = false

    private var prefs: Prefs { app.prefs }

    var body: some View {
        Form {
            serverSection
            playbackSection
            subtitleSection
            everythingSection
            appearanceSection
            librarySection
            aboutSection
        }
        .navigationTitle("Settings")
        .navigationBarTitleDisplayMode(.inline)
        .confirmationDialog("Sign out?", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) { Task { await session.signOut() } }
        }
        .confirmationDialog("Change server?", isPresented: $confirmChangeServer, titleVisibility: .visible) {
            Button("Sign out and change server", role: .destructive) { Task { await session.forgetServer() } }
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
            Toggle("Skip sponsor segments", isOn: bind(\.skipSponsors) { PrefsPatch(skipSponsors: $0) })
        } header: {
            Text("Playback")
        } footer: {
            Text("""
            Sponsor, self-promotion and interaction-reminder segments are \
            skipped automatically; other SponsorBlock categories are only \
            tinted on the scrubber.
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
            Button("Sign out", role: .destructive) { confirmSignOut = true }
        } header: {
            Text("Account")
        } footer: {
            Text(accountFooter)
        }
    }

    // MARK: - Binding helper

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
