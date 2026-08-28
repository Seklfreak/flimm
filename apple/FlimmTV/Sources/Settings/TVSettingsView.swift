import FlimmKit
import SwiftUI

/// Preferences live on the server (`GET /me`, `PATCH /me/prefs`), so every
/// change here follows the account to the phone and the web client.
///
/// Two of the phone's sections are missing on purpose. **Theme** is not offered:
/// tvOS has one system appearance and no per-app override, so a picker here
/// would be a control that does nothing. **Manage feeds** is not offered
/// either — see ``TVRoute``.
struct TVSettingsView: View {
    @Environment(AppModel.self) private var app
    @Environment(AuthSession.self) private var session
    /// Quality is per device, so it comes from here rather than from ``Prefs``.
    @Environment(PlaybackSettings.self) private var playback

    @State private var confirmSignOut = false
    @State private var confirmChangeServer = false

    private var prefs: Prefs { app.prefs }

    /// A section header with room under it. A focused row on tvOS grows into
    /// its neighbours, and a header with no gap ends up behind the white pill
    /// of the first row.
    private func sectionHeader(_ title: String) -> some View {
        Text(title).padding(.bottom, 12)
    }

    /// Explanatory text, held to a readable measure. A tvOS list row is nearly
    /// 1800pt wide; a paragraph that uses all of it is a 200-character line to
    /// read from a sofa.
    private func note(_ text: String) -> some View {
        Text(text)
            .font(.footnote)
            .foregroundStyle(.secondary)
            .frame(maxWidth: 1100, alignment: .leading)
    }

    var body: some View {
        List {
            playbackSection
            qualitySection
            subtitleSection
            everythingSection
            librarySection
            accountSection
        }
        .onAppear { Analytics.screen(.settings) }
        .confirmationDialog("Sign out?", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) { Task { await session.signOut() } }
            Button("Cancel", role: .cancel) {}
        }
        .confirmationDialog("Change server?", isPresented: $confirmChangeServer, titleVisibility: .visible) {
            Button("Sign out and change server", role: .destructive) { Task { await session.forgetServer() } }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("You'll be signed out and asked for a new server address.")
        }
    }

    // MARK: - Sections

    private var playbackSection: some View {
        Section {
            Toggle("Autoplay next video", isOn: bind(\.autoplay) { PrefsPatch(autoplay: $0) })
            TVOptionRow(title: "Playback speed", value: Fmt.speed(prefs.playbackSpeed)) {
                Task { await app.updatePrefs(PrefsPatch(playbackSpeed: PlaybackSpeeds.next(after: prefs.playbackSpeed))) }
            }
            Toggle("Skip sponsor segments", isOn: bind(\.skipSponsors) { PrefsPatch(skipSponsors: $0) })
            note("""
            Sponsor, self-promotion and interaction-reminder segments are \
            skipped automatically; other SponsorBlock categories are only \
            marked on the transport bar.
            """)
        } header: {
            sectionHeader("Playback")
        }
    }

    /// The one playback setting that stays on the device: an Apple TV on
    /// ethernet wants a different answer from a phone on cellular.
    private var qualitySection: some View {
        Section {
            NavigationLink {
                TVChoiceList(
                    title: "Video quality",
                    options: VideoQuality.options.map { ($0.rawValue, VideoQuality.label($0)) },
                    selection: playback.videoQuality.rawValue
                ) { value in
                    guard let preference = QualityPreference(rawValue: value) else { return }
                    playback.videoQuality = preference
                }
            } label: {
                LabeledContent("Quality", value: VideoQuality.label(playback.videoQuality))
            }
            note("""
            Auto plays the archived file whenever this Apple TV can decode it — \
            full quality, and nothing for the server to convert — and otherwise \
            the tallest rendition this screen can show, which is 4K on a 4K TV. \
            A fixed height always plays a converted rendition, falling to the \
            nearest lower one a video offers. This setting stays on this device.
            """)
        } header: {
            sectionHeader("Video quality")
        }
    }

    private var subtitleSection: some View {
        Section {
            NavigationLink {
                TVChoiceList(
                    title: "Subtitle language",
                    options: [(Prefs.subtitlesOff, "Off")] + languages.map { ($0, Fmt.langName($0)) },
                    selection: prefs.subtitleLang
                ) { value in
                    Task { await app.updatePrefs(PrefsPatch(subtitleLang: value)) }
                }
            } label: {
                LabeledContent("Language", value: subtitleLabel)
            }
            NavigationLink {
                TVChoiceList(
                    title: "Subtitle size",
                    options: SubtitleSize.allCases.map { ($0.rawValue, $0.rawValue.capitalized) },
                    selection: prefs.subtitleSize.rawValue
                ) { value in
                    guard let size = SubtitleSize(rawValue: value) else { return }
                    Task { await app.updatePrefs(PrefsPatch(subtitleSize: size)) }
                }
            } label: {
                LabeledContent("Size", value: prefs.subtitleSize.rawValue.capitalized)
            }
        } header: {
            sectionHeader("Subtitles")
        }
    }

    private var subtitleLabel: String {
        prefs.subtitleLang == Prefs.subtitlesOff ? "Off" : Fmt.langName(prefs.subtitleLang)
    }

    /// A code the server already holds that is not in the short list is added
    /// rather than being lost the next time this screen writes.
    private var languages: [String] {
        var all = SubtitleLanguages.common
        if !all.contains(prefs.subtitleLang), prefs.subtitleLang != Prefs.subtitlesOff {
            all.append(prefs.subtitleLang)
        }
        return all
    }

    private var everythingSection: some View {
        Section {
            NavigationLink {
                TVChoiceList(
                    title: "Sort",
                    options: FeedSort.allCases.map { ($0.rawValue, $0.label) },
                    selection: prefs.everythingSort.rawValue
                ) { value in
                    guard let sort = FeedSort(rawValue: value) else { return }
                    Task { await app.updatePrefs(PrefsPatch(everythingSort: sort)) }
                }
            } label: {
                LabeledContent("Sort", value: prefs.everythingSort.label)
            }
            Toggle("Hide seen", isOn: bind(\.everythingHideSeen) { PrefsPatch(everythingHideSeen: $0) })
            Toggle("Include Shorts", isOn: bind(\.everythingIncludeShorts) { PrefsPatch(everythingIncludeShorts: $0) })
            Text("The built-in feed over every channel. Its options are preferences, not feed settings.")
                .font(.footnote)
                .foregroundStyle(.secondary)
        } header: {
            sectionHeader("“Everything” feed")
        }
    }

    private var librarySection: some View {
        Section {
            Label("Edit feeds on your phone, iPad or the web", systemImage: "iphone")
                .foregroundStyle(.secondary)
            note("""
            Naming a feed and picking its channels needs a keyboard and a long \
            list. Apple TV shows feeds and plays them; it doesn't edit them.
            """)
        } header: {
            sectionHeader("Library")
        }
    }

    private var accountSection: some View {
        Section {
            LabeledContent("Signed in as", value: accountName)
            LabeledContent("Server", value: session.server?.baseURL.host() ?? "—")
            LabeledContent("Server version", value: session.server?.config.version ?? "—")
            LabeledContent("App version", value: TVConfig.displayVersion)
            if !session.requiresSignIn {
                note("""
                This server runs with authentication disabled: no sign-in, and \
                everyone who can reach it shares this account.
                """)
            }
            Button("Change server") { confirmChangeServer = true }
            if session.requiresSignIn {
                Button("Sign out", role: .destructive) { confirmSignOut = true }
            }
        } header: {
            sectionHeader("Account")
        }
    }

    private var accountName: String {
        guard let me = app.me else { return "—" }
        if !me.name.isEmpty { return me.name }
        return me.email.isEmpty ? "—" : me.email
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

/// A pushed list of options with a check on the current one — tvOS's own idiom
/// for a choice, and far easier on a remote than an inline picker.
struct TVChoiceList: View {
    let title: String
    let options: [(value: String, label: String)]
    let selection: String
    let choose: (String) -> Void

    @Environment(\.dismiss) private var dismiss

    var body: some View {
        List {
            Section(title) {
                ForEach(options, id: \.value) { option in
                    Button {
                        choose(option.value)
                        dismiss()
                    } label: {
                        HStack {
                            Text(option.label)
                            Spacer(minLength: 20)
                            if option.value == selection {
                                Image(systemName: "checkmark")
                                    .foregroundStyle(Palette.accent)
                            }
                        }
                    }
                }
            }
        }
    }
}

extension FeedSort {
    var label: String {
        switch self {
        case .newest: "Newest first"
        case .oldest: "Oldest first"
        case .shortest: "Shortest first"
        case .longest: "Longest first"
        }
    }
}
