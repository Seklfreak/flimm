import FlimmKit
import SwiftUI

/// Picks playlist sources ("series") for a feed: the channel directory first,
/// then one channel's archived playlists. Split in two levels because the
/// playlists are fetched per channel — a flat list would fetch them for every
/// channel in the archive just to draw the screen.
struct SeriesPickerView: View {
    @Binding var selection: Set<String>
    /// Channels already in the feed whole; their series are covered and are
    /// shown as such rather than offered again.
    let fullChannelIds: Set<String>

    @Environment(AppModel.self) private var app
    @State private var pager: Pager<ChannelSummary>?
    @State private var searchText = ""

    var body: some View {
        List {
            if let pager {
                if let error = pager.error, pager.items.isEmpty {
                    ErrorState(message: error) { Task { await pager.reload() } }
                        .listRowSeparator(.hidden)
                } else if pager.items.isEmpty && pager.hasLoaded {
                    EmptyState(icon: "person.2", title: "No channels", message: "Nothing matched that search.")
                        .listRowSeparator(.hidden)
                } else {
                    ForEach(pager.items) { channel in
                        row(channel)
                            .task { await pager.loadMoreIfNeeded(after: channel) }
                    }
                }
                if pager.isLoading || pager.isLoadingMore {
                    ProgressView().frame(maxWidth: .infinity)
                }
            }
        }
        .listStyle(.plain)
        .searchable(
            text: $searchText,
            placement: .navigationBarDrawer(displayMode: .always),
            prompt: "Search channels"
        )
        .navigationTitle(selection.isEmpty ? "Series" : "\(selection.count) series")
        .navigationBarTitleDisplayMode(.inline)
        .task(id: searchText) { await reload() }
    }

    private func row(_ channel: ChannelSummary) -> some View {
        NavigationLink {
            ChannelSeriesPicker(
                channel: channel,
                selection: $selection,
                channelIsInFeed: fullChannelIds.contains(channel.id)
            )
        } label: {
            HStack(spacing: 12) {
                ChannelAvatar(path: channel.thumbUrl, name: channel.name, size: 34)
                Text(channel.name)
                    .font(.subheadline.weight(.semibold))
                    .lineLimit(1)
                Spacer(minLength: 0)
            }
        }
    }

    private func reload() async {
        let client = app.client
        let text = searchText.trimmingCharacters(in: .whitespaces)
        if !text.isEmpty {
            try? await Task.sleep(for: .milliseconds(250))
            if Task.isCancelled { return }
        }
        let next = Pager<ChannelSummary> { page in
            try await client.channels(query: text.isEmpty ? nil : text, sort: .name, page: page)
        }
        pager = next
        await next.reload()
    }
}

/// One channel's playlists as a multi-select. A channel that is already in the
/// feed whole covers all of them, so the rows say so instead of toggling.
struct ChannelSeriesPicker: View {
    let channel: ChannelSummary
    @Binding var selection: Set<String>
    let channelIsInFeed: Bool

    @Environment(AppModel.self) private var app
    @State private var lists: [PlaylistSummary]?
    @State private var error: String?

    var body: some View {
        List {
            if channelIsInFeed {
                Text("The whole channel is in the feed already — every series is included.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            if let lists {
                if lists.isEmpty {
                    EmptyState(icon: "list.and.film", title: "No series", message: "No playlists are archived for this channel.")
                        .listRowSeparator(.hidden)
                } else {
                    ForEach(lists) { playlist in
                        row(playlist)
                    }
                }
            } else if let error {
                ErrorState(message: error) { Task { await load() } }
                    .listRowSeparator(.hidden)
            } else {
                ProgressView().frame(maxWidth: .infinity)
            }
        }
        .listStyle(.plain)
        .navigationTitle(channel.name)
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    private func row(_ playlist: PlaylistSummary) -> some View {
        Button {
            if selection.contains(playlist.id) {
                selection.remove(playlist.id)
            } else {
                selection.insert(playlist.id)
            }
        } label: {
            HStack(spacing: 12) {
                Image(systemName: selection.contains(playlist.id) ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(selection.contains(playlist.id) ? Palette.accent : Color.secondary)
                VStack(alignment: .leading, spacing: 1) {
                    Text(playlist.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Text(Fmt.plural(playlist.videoCount, "video"))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
        }
        .buttonStyle(.plain)
        .disabled(channelIsInFeed)
        .opacity(channelIsInFeed ? 0.45 : 1)
    }

    private func load() async {
        do {
            lists = try await app.client.channelPlaylists(channel.id)
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
    }
}
