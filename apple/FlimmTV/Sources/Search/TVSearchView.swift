import FlimmKit
import SwiftUI

/// Search across titles, channel names, playlist names and subtitle text.
///
/// A subtitle hit carries a timestamp and playing it starts there — the one
/// place a start position is passed explicitly, since resume is the default
/// everywhere else.
struct TVSearchView: View {
    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    @State private var query = ""
    @State private var scope: SearchScope = .all
    @State private var results: SearchResults?
    @State private var isSearching = false
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                TVScreenTitle(title: "Search")
                    .padding(.top, 20)
                Picker("Scope", selection: $scope) {
                    Text("Everything").tag(SearchScope.all)
                    Text("Titles").tag(SearchScope.titles)
                    Text("Subtitles").tag(SearchScope.subtitles)
                    Text("Channels").tag(SearchScope.channels)
                    Text("Playlists").tag(SearchScope.playlists)
                }
                .pickerStyle(.segmented)
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .searchable(text: $query, prompt: "Search videos, channels, subtitles")
        .task(id: searchKey) { await search() }
    }

    private var searchKey: String { "\(query)|\(scope.rawValue)" }

    @ViewBuilder
    private var content: some View {
        if let error {
            TVErrorState(message: error) { Task { await search() } }
        } else if isSearching && results == nil {
            TVLoadingState(label: "Searching…")
        } else if let results {
            if results.isEmpty {
                TVEmptyState(icon: "magnifyingglass", title: "No matches", message: "Nothing matched “\(query)”.")
            } else {
                list(results)
            }
        } else {
            TVEmptyState(
                icon: "magnifyingglass",
                title: "Search your archive",
                message: "Titles, channels, playlists and every archived subtitle line."
            )
        }
    }

    @ViewBuilder
    private func list(_ results: SearchResults) -> some View {
        if !results.channels.isEmpty {
            section("Channels", count: results.channels.total) {
                LazyVGrid(columns: TVGrids.tiles, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                    ForEach(results.channels.items) { match in
                        TVChannelCard(channel: match.channel)
                    }
                }
            }
        }
        if !results.playlists.isEmpty {
            section("Playlists", count: results.playlists.total) {
                LazyVGrid(columns: TVGrids.tiles, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                    ForEach(results.playlists.items) { match in
                        TVPlaylistCard(playlist: match.playlist)
                    }
                }
            }
        }
        if !results.videos.isEmpty {
            section("Videos", count: results.videos.total) {
                LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                    ForEach(results.videos.items) { match in
                        TVVideoMatchCard(match: match)
                    }
                }
            }
        }
    }

    private func section(
        _ title: String,
        count: Int,
        @ViewBuilder rows: () -> some View
    ) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Text(title).font(.title3.weight(.semibold))
                Text(Fmt.count(count))
                    .font(.headline)
                    .foregroundStyle(.secondary)
            }
            rows()
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
            results = try await app.client.search(text, scope: scope)
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
    }
}

/// A video result with its subtitle hits underneath; each hit seeks.
struct TVVideoMatchCard: View {
    let match: VideoMatch

    @Environment(TVPlayerCoordinator.self) private var player

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            TVVideoCard(video: match.video)
            ForEach(Array(match.subtitleHits.prefix(3).enumerated()), id: \.offset) { _, hit in
                Button {
                    player.play(match.video.id, startAt: hit.start)
                } label: {
                    HStack(alignment: .top, spacing: 10) {
                        Text(Fmt.duration(hit.start))
                            .font(.caption.monospacedDigit().weight(.bold))
                            .foregroundStyle(Palette.accent)
                        Text(hit.text)
                            .font(.caption)
                            .lineLimit(2)
                            .multilineTextAlignment(.leading)
                        Spacer(minLength: 0)
                    }
                }
                .buttonStyle(.borderless)
            }
        }
    }
}
