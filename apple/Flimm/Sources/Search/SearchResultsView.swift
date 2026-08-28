import FlimmKit
import SwiftUI

/// Search across titles, channel names, playlist names and subtitle text.
/// A subtitle hit carries a timestamp, and tapping it starts playback there —
/// the one place `t=` exists, since resume is the default everywhere else.
struct SearchResultsView: View {
    let query: String
    var feedId: String?

    @Environment(AppModel.self) private var app
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass

    @State private var results: SearchResults?
    @State private var scope: SearchScope = .all
    @State private var unseenOnly = false
    @State private var inCurrentFeed = false
    @State private var isSearching = false
    @State private var error: String?

    var body: some View {
        VStack(spacing: 0) {
            filters
            content
        }
        .onAppear { Analytics.screen(.search) }
        .task(id: searchKey) { await search() }
    }

    /// Any change here means a new request; the debounce lives in `search()`.
    private var searchKey: String { "\(query)|\(scope.rawValue)|\(unseenOnly)|\(inCurrentFeed)" }

    private var filters: some View {
        VStack(alignment: .leading, spacing: 8) {
            ChipPicker(
                options: [
                    (SearchScope.all, "Everything"),
                    (SearchScope.titles, "Titles"),
                    (SearchScope.subtitles, "Subtitles"),
                    (SearchScope.channels, "Channels"),
                    (SearchScope.playlists, "Playlists")
                ],
                selection: $scope
            )
            // Two things were wrong here, not one. `Toggle("title", isOn:)`
            // paired with `.labelsHidden()` silently drops its own label in
            // this position — the same reason `UpNextList`'s Autoplay toggle
            // (PlayerDetails.swift) never uses that initializer and draws a
            // `Text` of its own instead; the closure form below (`isOn:` with
            // an explicit, empty label) doesn't have the problem. On top of
            // that, an `HStack` of two such rows squeezes both labels down to
            // nothing at the narrowest widths and with Dynamic Type turned up
            // (the same lesson as the tvOS picker in docs/apple-apps.md — size
            // to the label, don't guess a budget for it), so each row is
            // stacked and given the full row width rather than sharing one.
            VStack(alignment: .leading, spacing: 8) {
                filterToggle("Unseen only", isOn: $unseenOnly)
                if feedId != nil {
                    filterToggle("This feed", isOn: $inCurrentFeed)
                }
            }
            .font(.footnote)
            .padding(.horizontal, 16)
        }
        .padding(.vertical, 8)
    }

    private func filterToggle(_ title: String, isOn: Binding<Bool>) -> some View {
        HStack {
            Text(title)
            Spacer(minLength: 12)
            Toggle(isOn: isOn) { EmptyView() }
        }
        // This row sits directly in a `.overlay` (`FeedsView`), which never
        // proposes it a definite width — a `maxWidth: .infinity` here is
        // simply ignored, and without a floor the row shrinks to the label's
        // compressed width instead of the row width, wrapping "Unseen only"
        // one word per line. A `minWidth` is a concrete lower bound the row
        // always gets, comfortably under the narrowest iPhone's width, so the
        // label lays out on one line there and wraps normally, not
        // word-by-word, if Dynamic Type pushes past it.
        .frame(minWidth: 260, alignment: .leading)
    }

    @ViewBuilder
    private var content: some View {
        if let error {
            ErrorState(message: error) { Task { await search() } }
        } else if isSearching && results == nil {
            LoadingState(label: "Searching…")
        } else if let results {
            if results.isEmpty {
                EmptyState(icon: "magnifyingglass", title: "No matches", message: "Nothing matched “\(query)”.")
            } else {
                list(results)
            }
        } else {
            Spacer()
        }
    }

    private func list(_ results: SearchResults) -> some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 20) {
                if !results.channels.isEmpty {
                    section("Channels", count: results.channels.total) {
                        ForEach(results.channels.items) { match in
                            NavigationLink(value: Route.channel(match.channel.id)) {
                                ChannelRow(channel: match.channel)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                if !results.playlists.isEmpty {
                    section("Playlists", count: results.playlists.total) {
                        ForEach(results.playlists.items) { match in
                            NavigationLink(value: Route.playlist(match.playlist.id)) {
                                PlaylistRow(playlist: match.playlist)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                }
                if !results.videos.isEmpty {
                    section("Videos", count: results.videos.total) {
                        if horizontalSizeClass == .regular {
                            LazyVGrid(columns: Grids.tiles, alignment: .leading, spacing: Grids.spacing) {
                                videoMatches(results)
                            }
                        } else {
                            videoMatches(results)
                        }
                    }
                }
                Text("\(results.tookMs) ms")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                    .padding(.horizontal, 16)
            }
            .padding(.vertical, 12)
        }
    }

    @ViewBuilder
    private func videoMatches(_ results: SearchResults) -> some View {
        ForEach(results.videos.items) { match in
            VideoMatchRow(match: match, onDismissChange: updateVideo)
        }
    }

    /// Search keeps listing a video after it is dismissed — the contract
    /// makes an exception for feeds only — so this patches the row's own
    /// state in place rather than removing it.
    private func updateVideo(_ updated: VideoSummary) {
        guard let current = results,
              let index = current.videos.items.firstIndex(where: { $0.video.id == updated.id }) else { return }
        var items = current.videos.items
        items[index] = VideoMatch(video: updated, subtitleHits: items[index].subtitleHits)
        results = SearchResults(
            tookMs: current.tookMs,
            videos: SearchSection(total: current.videos.total, items: items),
            channels: current.channels,
            playlists: current.playlists
        )
        Task { await app.videoListStateChanged() }
    }

    private func section(
        _ title: String,
        count: Int,
        @ViewBuilder rows: () -> some View
    ) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text(title).font(.headline)
                Text(Fmt.count(count))
                    .font(.caption.weight(.semibold))
                    .foregroundStyle(.secondary)
            }
            .padding(.horizontal, 16)
            VStack(alignment: .leading, spacing: 12) {
                rows()
            }
            .padding(.horizontal, 16)
        }
    }

    private func search() async {
        let text = query.trimmingCharacters(in: .whitespaces)
        guard text.count >= 2 else {
            results = nil
            return
        }
        try? await Task.sleep(for: .milliseconds(300))
        if Task.isCancelled { return }
        isSearching = true
        defer { isSearching = false }
        do {
            results = try await app.client.search(
                text,
                scope: scope,
                unseen: unseenOnly,
                feed: inCurrentFeed ? feedId : nil
            )
            error = nil
            // After the debounce and the request, so this counts searches
            // that ran — and the scope only, never what was typed.
            Analytics.search(scope: scope.rawValue)
        } catch {
            self.error = AppModel.message(for: error)
        }
    }
}

/// A video result, with its subtitle hits underneath. Each hit seeks.
struct VideoMatchRow: View {
    let match: VideoMatch
    var onDismissChange: ((VideoSummary) -> Void)?

    @Environment(PlayerCoordinator.self) private var player

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            VideoRow(video: match.video, onDismissChange: onDismissChange)
            ForEach(Array(match.subtitleHits.enumerated()), id: \.offset) { _, hit in
                Button {
                    player.play(match.video.id, startAt: hit.start)
                } label: {
                    HStack(alignment: .top, spacing: 8) {
                        Text(Fmt.duration(hit.start))
                            .font(.caption.monospacedDigit().weight(.semibold))
                            .foregroundStyle(Palette.accent)
                        Text(hit.text)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                            .lineLimit(2)
                            .multilineTextAlignment(.leading)
                        Spacer(minLength: 0)
                    }
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Palette.raised, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                }
                .buttonStyle(.plain)
            }
        }
    }
}
