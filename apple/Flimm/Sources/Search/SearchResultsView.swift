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
        .task(id: searchKey) { await search() }
    }

    /// Any change here means a new request; the debounce lives in `search()`.
    private var searchKey: String { "\(query)|\(scope.rawValue)|\(unseenOnly)|\(inCurrentFeed)" }

    private var filters: some View {
        VStack(spacing: 8) {
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
            HStack(spacing: 12) {
                Toggle("Unseen only", isOn: $unseenOnly)
                    .font(.footnote)
                if feedId != nil {
                    Toggle("This feed", isOn: $inCurrentFeed)
                        .font(.footnote)
                }
            }
            .toggleStyle(.switch)
            .padding(.horizontal, 16)
        }
        .padding(.vertical, 8)
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
            VideoMatchRow(match: match)
        }
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
        } catch {
            self.error = AppModel.message(for: error)
        }
    }
}

/// A video result, with its subtitle hits underneath. Each hit seeks.
struct VideoMatchRow: View {
    let match: VideoMatch

    @Environment(PlayerCoordinator.self) private var player

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            VideoRow(video: match.video)
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
