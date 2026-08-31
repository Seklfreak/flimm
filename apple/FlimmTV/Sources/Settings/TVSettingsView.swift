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
    /// Loaded once when the section appears; see `statsSection`.
    @State private var stats: WatchStats?
    @State private var isPublishing = false
    @State private var publishResult: String?
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
    /// Crowd-sourced titles and thumbnails, each on its own row because they
    /// are set apart from each other.
    private var dearrowSection: some View {
        Section {
            TVOptionRow(title: "Titles", value: prefs.dearrowTitles.label) {
                Task { await app.updatePrefs(PrefsPatch(dearrowTitles: prefs.dearrowTitles.next)) }
            }
            TVOptionRow(title: "Thumbnails", value: prefs.dearrowThumbnails.label) {
                Task { await app.updatePrefs(PrefsPatch(dearrowThumbnails: prefs.dearrowThumbnails.next)) }
            }
            note("""
            Crowd-sourced titles and thumbnails. Manual uses what people submitted and voted on; \
            All also tidies a shouted title and picks a frame where nobody has. The frame is cut \
            from your own copy of the video — DeArrow supplies a timestamp, never an image.
            """)
        } header: {
            Text("DeArrow")
        }
    }

    /// A remote has no picker, so a row cycles: Skip → Ask → Off → Skip. The
    /// whole map goes back, because the server replaces what it is sent.
    private func cycleSponsor(_ category: String) {
        let order: [SponsorSetting] = [.skip, .ask, .off]
        let current = prefs.sponsorActions[category] ?? .off
        let next = order[((order.firstIndex(of: current) ?? 0) + 1) % order.count]
        var actions = prefs.sponsorActions
        actions[category] = next
        Task { await app.updatePrefs(PrefsPatch(sponsorActions: actions)) }
    }

    private func sponsorLabel(_ setting: SponsorSetting) -> String {
        switch setting {
        case .skip: "Skip"
        case .ask: "Ask"
        case .off: "Off"
        }
    }

    /// What the Home screen would show, read back from the shared container.
    ///
    /// The three answers are three different problems. No container at all is
    /// an entitlement that did not survive signing — nothing the app can fix.
    /// A container with nothing in it means the app never got as far as
    /// writing. Numbers mean the app's half works and anything still missing
    /// on the Home screen is tvOS not asking, which is what happens when Flimm
    /// is not in the top row.
    private var topShelfStatus: String {
        guard UserDefaults(suiteName: TopShelfStore.appGroup) != nil else {
            return "unavailable — no app group"
        }
        guard let snapshot = TopShelfStore.read(), !snapshot.entries.isEmpty else {
            return "nothing published yet · would publish \(app.launchFeed?.name ?? "no feed")"
        }
        return "\(snapshot.entries.count) from \(snapshot.feedName) · \(Fmt.relativeDay(snapshot.updatedAt))"
    }

    /// Publishes the shelf on demand, the same way launching the app does, and
    /// says exactly which step gave up if one did.
    private func publishTopShelf() async {
        isPublishing = true
        defer { isPublishing = false }
        publishResult = await TopShelfRefresh.publishLaunchFeed(app: app).message
    }

    private func note(_ text: String) -> some View {
        Text(text)
            .font(.footnote)
            .foregroundStyle(.secondary)
            .frame(maxWidth: 1100, alignment: .leading)
    }

    var body: some View {
        List {
            playbackSection
            dearrowSection
            qualitySection
            subtitleSection
            everythingSection
            statsSection
            librarySection
            accountSection
        }
        .onAppear { Analytics.screen(.settings) }
        .confirmationDialog("Sign out?", isPresented: $confirmSignOut, titleVisibility: .visible) {
            Button("Sign out", role: .destructive) {
                // The top shelf lives on the Home screen, outside the app, so
                // signing out has to take it down too.
                TopShelfRefresh.clear()
                Task { await session.signOut() }
            }
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
            Toggle("Even out the volume", isOn: bind(\.normalizeLoudness) { PrefsPatch(normalizeLoudness: $0) })
            note("""
            Measures each video once and turns the loud ones down, so you stop \
            reaching for the volume between channels. Nothing is ever turned up.
            """)
            Toggle("SponsorBlock", isOn: bind(\.skipSponsors) { PrefsPatch(skipSponsors: $0) })
            note("""
            The master switch. Off, and no segment is skipped, muted or \
            offered — they are still marked on the transport bar.
            """)
            if prefs.skipSponsors {
                ForEach(SponsorSetting.categories, id: \.self) { category in
                    TVOptionRow(
                        title: SponsorRules.label(category),
                        value: sponsorLabel(prefs.sponsorActions[category] ?? .off)
                    ) {
                        cycleSponsor(category)
                    }
                }
                note("Skip jumps the segment, Ask offers it while it plays, Off leaves it alone.")
            }
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

    /// The headline numbers only. The phone and the web draw the charts; a
    /// remote has nothing to hover and nothing to tap on a bar, so the TV shows
    /// what can be read from the sofa and says where the rest is.
    private var statsSection: some View {
        Section {
            if let stats, stats.started > 0 {
                LabeledContent("Watched", value: Fmt.durationLong(stats.seconds))
                LabeledContent("Videos started", value: Fmt.compact(stats.started))
                LabeledContent("Finished", value: Fmt.compact(stats.finished))
                if let rate = stats.finishRate {
                    LabeledContent("Finish rate", value: "\(Int((rate * 100).rounded()))%")
                }
                ForEach(stats.topChannels.prefix(3)) { channel in
                    LabeledContent(channel.name, value: Fmt.durationLong(channel.seconds))
                }
                note("""
                “Watched” is the furthest point reached in each video, added up: a finished video counts in full, \
                an abandoned one counts where it stopped, and watching something twice counts once. The charts are \
                on your phone, iPad or the web.
                """)
            } else if stats != nil {
                Label("Nothing watched yet", systemImage: "chart.bar").foregroundStyle(.secondary)
            } else {
                ProgressView()
            }
        } header: {
            sectionHeader("Stats")
        }
        .task {
            guard stats == nil else { return }
            stats = try? await app.client.stats()
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
            // The top shelf is drawn by tvOS, outside the app, from a snapshot
            // the app leaves in a shared container. When it shows nothing there
            // is otherwise no way to tell whether the app failed to write it or
            // the Home screen simply is not asking — which is the difference
            // between a bug here and Flimm not being in the top row.
            LabeledContent("Top shelf", value: topShelfStatus)
            // Publishing normally happens when the pinned feed is shown. Doing
            // it from here as well is what tells "the app cannot write" apart
            // from "the app never ran the code that writes".
            Button("Publish to top shelf now") { Task { await publishTopShelf() } }
                .disabled(isPublishing)
            if let publishResult { note(publishResult) }
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
