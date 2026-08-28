import FlimmKit
import SwiftUI

/// Custom playlists and the ones archived from channels, pinned first. A pin is
/// Flimm's own per-user state, so it follows the account here from wherever it
/// was set.
struct TVPlaylistsView: View {
    @Environment(AppModel.self) private var app

    @State private var pager: Pager<PlaylistSummary>?
    @State private var kind: PlaylistKind?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                HStack(alignment: .bottom) {
                    TVScreenTitle(title: "Playlists")
                    Spacer(minLength: 40)
                    Picker("Kind", selection: $kind) {
                        Text("All").tag(PlaylistKind?.none)
                        Text("Custom").tag(PlaylistKind?.some(.custom))
                        Text("From channels").tag(PlaylistKind?.some(.channel))
                    }
                    .pickerStyle(.segmented)
                    .fixedSize()
                }
                .padding(.top, 20)

                if !app.pinnedPlaylists.isEmpty {
                    section("Pinned", playlists: app.pinnedPlaylists)
                }
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .onAppear { Analytics.screen(.playlists) }
        .task(id: kind) { await reload() }
    }

    @ViewBuilder
    private var content: some View {
        if let pager {
            if let error = pager.error, pager.items.isEmpty {
                TVErrorState(message: error) { Task { await pager.reload() } }
            } else if pager.items.isEmpty && pager.hasLoaded {
                TVEmptyState(icon: "list.and.film", title: "No playlists")
            } else {
                VStack(alignment: .leading, spacing: 16) {
                    if !app.pinnedPlaylists.isEmpty {
                        Text("All playlists")
                            .font(.title3.weight(.semibold))
                            .foregroundStyle(.secondary)
                    }
                    grid(pager.items) { playlist in
                        Task { await pager.loadMoreIfNeeded(after: playlist) }
                    }
                }
                if pager.isLoadingMore {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 24)
                }
            }
        } else {
            TVLoadingState()
        }
    }

    private func section(_ title: String, playlists: [PlaylistSummary]) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(title)
                .font(.title3.weight(.semibold))
                .foregroundStyle(.secondary)
            grid(playlists) { _ in }
        }
    }

    private func grid(_ playlists: [PlaylistSummary], onAppear: @escaping (PlaylistSummary) -> Void) -> some View {
        LazyVGrid(columns: TVGrids.tiles, alignment: .leading, spacing: TVMetrics.gridSpacing) {
            ForEach(playlists) { playlist in
                TVPlaylistCard(playlist: playlist)
                    .onAppear { onAppear(playlist) }
            }
        }
    }

    private func reload() async {
        let client = app.client
        let kind = kind
        let key = "tv-playlists:\(kind?.rawValue ?? "all")"
        await app.refreshPinnedPlaylists()
        if let cached: Pager<PlaylistSummary> = app.pagers.existing(key) {
            pager = cached
            return
        }
        let next = Pager<PlaylistSummary> { page in
            try await client.playlists(kind: kind, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }
}
