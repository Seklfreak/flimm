import FlimmKit
import SwiftUI

/// History, grouped by day and newest first. Swiping a row hides the entry;
/// that is a soft delete on the server and deliberately does *not* change the
/// video's seen state.
struct HistoryView: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @State private var pager: Pager<HistoryEntry>?
    @State private var filter: HistoryFilter = .all
    @State private var searchText = ""

    var body: some View {
        List {
            if let pager {
                if let error = pager.error, pager.items.isEmpty {
                    ErrorState(message: error) { Task { await pager.reload() } }
                        .listRowSeparator(.hidden)
                } else if pager.items.isEmpty && pager.hasLoaded {
                    EmptyState(
                        icon: "clock.arrow.circlepath",
                        title: "Nothing here yet",
                        message: "Videos appear once you have watched a little of them."
                    )
                    .listRowSeparator(.hidden)
                } else {
                    ForEach(groups, id: \.heading) { group in
                        Section(group.heading) {
                            ForEach(group.entries) { entry in
                                VideoRow(
                                    video: entry.video,
                                    subtitle: subtitle(for: entry),
                                    onDismissChange: { updateEntry(entry, video: $0) }
                                )
                                .task { await pager.loadMoreIfNeeded(after: entry) }
                                .swipeActions(edge: .trailing) {
                                    Button(role: .destructive) {
                                        Task { await remove(entry) }
                                    } label: {
                                        Label("Remove", systemImage: "trash")
                                    }
                                }
                                .swipeActions(edge: .leading) {
                                    // Same idiom as the trailing "Remove" swipe,
                                    // for the other per-user state a history
                                    // row can flip: keeping the video out of
                                    // feeds without touching whether it was
                                    // watched.
                                    Button {
                                        Task { await toggleDismiss(entry) }
                                    } label: {
                                        if entry.video.dismissed {
                                            Label("Add back", systemImage: "arrow.uturn.backward")
                                        } else {
                                            Label("Not interested", systemImage: "hand.thumbsdown")
                                        }
                                    }
                                    .tint(entry.video.dismissed ? .blue : .orange)
                                }
                            }
                        }
                    }
                }
                if pager.isLoading || pager.isLoadingMore {
                    ProgressView().frame(maxWidth: .infinity)
                }
            } else {
                LoadingState().listRowSeparator(.hidden)
            }
        }
        .listStyle(.plain)
        .refreshable { await pager?.reload() }
        .navigationTitle("History")
        .searchable(text: $searchText, isPresented: nav.searchPresented(for: .history), prompt: "Search history")
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Picker("Filter", selection: $filter) {
                    Text("All").tag(HistoryFilter.all)
                    Text("In progress").tag(HistoryFilter.inProgress)
                    Text("Seen").tag(HistoryFilter.seen)
                }
                .pickerStyle(.menu)
            }
        }
        .task(id: queryKey) { await reload() }
    }

    private var queryKey: String { "\(filter.rawValue)|\(searchText)" }

    private struct DayGroup {
        let heading: String
        let entries: [HistoryEntry]
    }

    private var groups: [DayGroup] {
        var order: [String] = []
        var buckets: [String: [HistoryEntry]] = [:]
        for entry in pager?.items ?? [] {
            let heading = Fmt.dayHeading(entry.playedAt)
            if buckets[heading] == nil {
                buckets[heading] = []
                order.append(heading)
            }
            buckets[heading]?.append(entry)
        }
        return order.map { DayGroup(heading: $0, entries: buckets[$0] ?? []) }
    }

    private func subtitle(for entry: HistoryEntry) -> String {
        let video = entry.video
        if entry.state == .inProgress {
            return "\(video.channel.name) · \(Fmt.duration(video.position)) / \(Fmt.duration(video.duration))"
        }
        return "\(video.channel.name) · \(Fmt.seenLabel(entry.playedAt))"
    }

    private func reload() async {
        let client = app.client
        let text = searchText.trimmingCharacters(in: .whitespaces)
        let filter = filter
        let key = "history:\(filter.rawValue)|\(text)"
        if let cached: Pager<HistoryEntry> = app.pagers.existing(key) {
            pager = cached
            return
        }
        if !text.isEmpty {
            try? await Task.sleep(for: .milliseconds(250))
            if Task.isCancelled { return }
        }
        let next = Pager<HistoryEntry> { page in
            try await client.history(filter: filter, query: text.isEmpty ? nil : text, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }

    private func remove(_ entry: HistoryEntry) async {
        pager?.remove(id: entry.id)
        try? await app.client.deleteHistoryEntry(entry.id)
    }

    private func toggleDismiss(_ entry: HistoryEntry) async {
        guard let updated = await toggleDismissed(entry.video, client: app.client) else { return }
        updateEntry(entry, video: updated)
    }

    /// History keeps listing a video after it is dismissed — the contract
    /// makes an exception for feeds only — so this patches the row's own
    /// state in place rather than removing it.
    private func updateEntry(_ entry: HistoryEntry, video: VideoSummary) {
        pager?.replace(HistoryEntry(id: entry.id, video: video, playedAt: entry.playedAt, state: entry.state))
        Task { await app.videoListStateChanged() }
    }
}
