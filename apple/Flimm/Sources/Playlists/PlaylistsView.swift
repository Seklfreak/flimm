import FlimmKit
import SwiftUI

/// Custom playlists and the ones archived from channels, side by side. Pinned
/// ones come first — a pin is Flimm's own per-user state, so it follows the
/// account across clients.
///
/// A phone gets the rows; a wide window gets a grid of the same playlists,
/// because artwork is most of what tells them apart.
struct PlaylistsView: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @Environment(\.horizontalSizeClass) private var horizontalSizeClass
    @State private var pager: Pager<PlaylistSummary>?
    @State private var kind: PlaylistKind?
    @State private var searchText = ""
    @State private var showNewPlaylist = false
    @State private var newName = ""

    private var visible: [PlaylistSummary] {
        let all = pager?.items ?? []
        let text = searchText.trimmingCharacters(in: .whitespaces)
        guard !text.isEmpty else { return all }
        return all.filter { $0.name.localizedCaseInsensitiveContains(text) }
    }

    private var showsPinned: Bool {
        !app.pinnedPlaylists.isEmpty && searchText.isEmpty
    }

    var body: some View {
        Group {
            if horizontalSizeClass == .regular {
                grid
            } else {
                rows
            }
        }
        .refreshable {
            await app.refreshPinnedPlaylists()
            await pager?.reload()
        }
        .navigationTitle("Playlists")
        .onAppear { Analytics.screen(.playlists) }
        .searchable(text: $searchText, isPresented: nav.searchPresented(for: .playlists), prompt: "Filter playlists")
        .toolbar { toolbar }
        .alert("New playlist", isPresented: $showNewPlaylist) {
            TextField("Name", text: $newName)
            Button("Cancel", role: .cancel) { newName = "" }
            Button("Create") { Task { await create() } }
        } message: {
            Text("Custom playlists are created in TubeArchivist, so they exist there too.")
        }
        .task(id: kind) { await reload() }
    }

    // MARK: - Layouts

    private var rows: some View {
        List {
            if showsPinned {
                Section("Pinned") {
                    ForEach(app.pinnedPlaylists) { playlist in
                        NavigationLink(value: Route.playlist(playlist.id)) {
                            PlaylistRow(playlist: playlist)
                        }
                    }
                }
            }
            Section {
                if let pager {
                    if let error = pager.error, pager.items.isEmpty {
                        ErrorState(message: error) { Task { await pager.reload() } }
                            .listRowSeparator(.hidden)
                    } else if visible.isEmpty && pager.hasLoaded {
                        EmptyState(icon: "list.and.film", title: "No playlists")
                            .listRowSeparator(.hidden)
                    }
                    ForEach(visible) { playlist in
                        NavigationLink(value: Route.playlist(playlist.id)) {
                            PlaylistRow(playlist: playlist)
                        }
                        .task { await pager.loadMoreIfNeeded(after: playlist) }
                    }
                    if pager.isLoading || pager.isLoadingMore {
                        ProgressView().frame(maxWidth: .infinity)
                    }
                }
            }
        }
        .listStyle(.insetGrouped)
    }

    private var grid: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                if showsPinned {
                    section("Pinned", playlists: app.pinnedPlaylists)
                }
                if let pager {
                    if let error = pager.error, pager.items.isEmpty {
                        ErrorState(message: error) { Task { await pager.reload() } }
                    } else if visible.isEmpty && pager.hasLoaded {
                        EmptyState(icon: "list.and.film", title: "No playlists")
                    } else {
                        section(showsPinned ? "All playlists" : nil, playlists: visible, pager: pager)
                    }
                    if pager.isLoading || pager.isLoadingMore {
                        ProgressView().frame(maxWidth: .infinity)
                    }
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
        }
        .background(Palette.background)
    }

    @ViewBuilder
    private func section(
        _ title: String?,
        playlists: [PlaylistSummary],
        pager: Pager<PlaylistSummary>? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            if let title {
                Text(title).font(.headline)
            }
            LazyVGrid(columns: Grids.tiles, alignment: .leading, spacing: Grids.spacing) {
                ForEach(playlists) { playlist in
                    NavigationLink(value: Route.playlist(playlist.id)) {
                        PlaylistCard(playlist: playlist)
                    }
                    .buttonStyle(.plain)
                    .task {
                        guard let pager else { return }
                        await pager.loadMoreIfNeeded(after: playlist)
                    }
                }
            }
        }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Menu {
                Picker("Kind", selection: $kind) {
                    Text("All").tag(PlaylistKind?.none)
                    Text("Custom").tag(PlaylistKind?.some(.custom))
                    Text("From channels").tag(PlaylistKind?.some(.channel))
                }
                Divider()
                Button {
                    showNewPlaylist = true
                } label: {
                    Label("New playlist", systemImage: "plus")
                }
            } label: {
                Image(systemName: "ellipsis.circle")
            }
        }
    }

    private func reload() async {
        let client = app.client
        let kind = kind
        let key = "playlists:\(kind?.rawValue ?? "all")"
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

    private func create() async {
        let name = newName.trimmingCharacters(in: .whitespaces)
        newName = ""
        guard !name.isEmpty else { return }
        _ = try? await app.client.createPlaylist(name: name)
        app.pagers.removeAll()
        await reload()
    }
}

extension PlaylistSummary {
    /// "12 videos · 3h 40m · 4 unseen" — a music playlist reports no watch
    /// state at all, so it never claims any (docs/api.md, "Music playlists").
    var metaLine: String {
        var parts = [Fmt.plural(videoCount, "video")]
        if totalDuration > 0 { parts.append(Fmt.durationLong(totalDuration)) }
        if music {
            return parts.joined(separator: " · ")
        }
        let remaining = Fmt.remainingUnseen(videoCount: videoCount, seenCount: seenCount)
        if remaining > 0 { parts.append("\(Fmt.count(remaining)) unseen") }
        return parts.joined(separator: " · ")
    }
}

struct PlaylistRow: View {
    let playlist: PlaylistSummary

    var body: some View {
        HStack(spacing: 12) {
            MediaImage(path: playlist.thumbUrl)
                .frame(width: 76, height: 44)
                .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 5) {
                    if playlist.music {
                        Image(systemName: "music.note")
                            .font(.caption2)
                            .foregroundStyle(Palette.accent)
                    }
                    Text(playlist.name)
                        .font(.subheadline.weight(.bold))
                        .lineLimit(1)
                }
                Text(playlist.metaLine)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
                // A music playlist reports no watch state at all, so a progress
                // bar there would always read zero (docs/api.md, "Music playlists").
                if !playlist.music && playlist.progress > 0 {
                    ProgressBar(value: playlist.progress)
                        .frame(maxWidth: 140)
                }
            }
            Spacer(minLength: 0)
            if playlist.pinned {
                Image(systemName: "pin.fill")
                    .font(.caption2)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(.vertical, 2)
    }
}

/// The grid cell for a wide window: artwork first, the way the web client's
/// playlist grid reads.
struct PlaylistCard: View {
    let playlist: PlaylistSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            MediaImage(path: playlist.thumbUrl)
                .aspectRatio(16 / 9, contentMode: .fill)
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                .overlay(alignment: .topTrailing) {
                    if playlist.pinned {
                        Image(systemName: "pin.fill")
                            .pillStyle()
                            .padding(8)
                    }
                }
            HStack(spacing: 5) {
                if playlist.music {
                    Image(systemName: "music.note")
                        .font(.caption)
                        .foregroundStyle(Palette.accent)
                }
                Text(playlist.name)
                    .font(.subheadline.weight(.bold))
                    .lineLimit(2)
                    .multilineTextAlignment(.leading)
            }
            Text(playlist.metaLine)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            if !playlist.music && playlist.progress > 0 {
                ProgressBar(value: playlist.progress)
            }
        }
        .contentShape(Rectangle())
    }
}

/// The square-ish tile used in the channel page's playlist strip.
struct PlaylistTile: View {
    let playlist: PlaylistSummary

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            MediaImage(path: playlist.thumbUrl)
                .frame(width: 150, height: 84)
                .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
            Text(playlist.name)
                .font(.caption.weight(.bold))
                .lineLimit(2)
                .multilineTextAlignment(.leading)
            Text(Fmt.plural(playlist.videoCount, "video"))
                .font(.caption2)
                .foregroundStyle(.secondary)
        }
        .frame(width: 150, alignment: .leading)
    }
}
