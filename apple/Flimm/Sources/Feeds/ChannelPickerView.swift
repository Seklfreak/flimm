import FlimmKit
import SwiftUI

/// Multi-select over the channel directory, with the same search the directory
/// uses and each channel's existing feed memberships shown — picking channels
/// for a feed is easier when you can see which feeds already have them.
struct ChannelPickerView: View {
    @Binding var selection: Set<String>

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
        // Always on show. The default placement folds the field into the
        // navigation bar, where it only appears if you happen to drag the list
        // down — so a directory of hundreds of channels looks like it has no
        // search at all.
        .searchable(
            text: $searchText,
            placement: .navigationBarDrawer(displayMode: .always),
            prompt: "Search channels"
        )
        .navigationTitle(Fmt.plural(selection.count, "channel"))
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button("Select all shown") {
                        selection.formUnion((pager?.items ?? []).map(\.id))
                    }
                    Button("Clear selection", role: .destructive) { selection.removeAll() }
                } label: {
                    Image(systemName: "checklist")
                }
            }
        }
        .task(id: searchText) { await reload() }
    }

    private func row(_ channel: ChannelSummary) -> some View {
        Button {
            if selection.contains(channel.id) {
                selection.remove(channel.id)
            } else {
                selection.insert(channel.id)
            }
        } label: {
            HStack(spacing: 12) {
                Image(systemName: selection.contains(channel.id) ? "checkmark.circle.fill" : "circle")
                    .foregroundStyle(selection.contains(channel.id) ? Palette.accent : Color.secondary)
                ChannelAvatar(path: channel.thumbUrl, name: channel.name, size: 34)
                VStack(alignment: .leading, spacing: 1) {
                    Text(channel.name)
                        .font(.subheadline.weight(.semibold))
                        .lineLimit(1)
                    Text(membership(channel))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 0)
            }
        }
        .buttonStyle(.plain)
    }

    private func membership(_ channel: ChannelSummary) -> String {
        let others = channel.feeds.filter { $0.id != Feed.everythingID }.map(\.name)
        let counts = Fmt.plural(channel.videoCount, "video")
        return others.isEmpty ? "\(counts) · not in a feed" : "\(counts) · in \(others.joined(separator: ", "))"
    }

    private func reload() async {
        let client = app.client
        let text = searchText.trimmingCharacters(in: .whitespaces)
        // Debounce: typing shouldn't fire a request per keystroke.
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
