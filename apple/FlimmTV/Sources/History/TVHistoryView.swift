import FlimmKit
import SwiftUI

/// History, grouped by day and newest first.
///
/// Removing an entry is a soft delete on the server and deliberately does *not*
/// change the video's seen state — but there is no swipe on a remote, so the
/// TV only reads history. Tidying it up happens where the gesture exists.
struct TVHistoryView: View {
    @Environment(AppModel.self) private var app

    @State private var pager: Pager<HistoryEntry>?
    @State private var filter: HistoryFilter = .all

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                HStack(alignment: .bottom) {
                    TVScreenTitle(title: "History")
                    Spacer(minLength: 40)
                    Picker("Filter", selection: $filter) {
                        Text("All").tag(HistoryFilter.all)
                        Text("In progress").tag(HistoryFilter.inProgress)
                        Text("Seen").tag(HistoryFilter.seen)
                    }
                    .pickerStyle(.segmented)
                    .fixedSize()
                }
                .padding(.top, 20)
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .task(id: filter) { await reload() }
    }

    @ViewBuilder
    private var content: some View {
        if let pager {
            if let error = pager.error, pager.items.isEmpty {
                TVErrorState(message: error) { Task { await pager.reload() } }
            } else if pager.items.isEmpty && pager.hasLoaded {
                TVEmptyState(
                    icon: "clock.arrow.circlepath",
                    title: "Nothing here yet",
                    message: "Videos appear once you have watched a little of them."
                )
            } else {
                ForEach(groups(pager.items), id: \.heading) { group in
                    VStack(alignment: .leading, spacing: 16) {
                        Text(group.heading)
                            .font(.title3.weight(.semibold))
                            .foregroundStyle(.secondary)
                        LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                            ForEach(group.entries) { entry in
                                TVVideoCard(video: entry.video)
                                    .task { await pager.loadMoreIfNeeded(after: entry) }
                            }
                        }
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

    private struct DayGroup {
        let heading: String
        let entries: [HistoryEntry]
    }

    /// The server already returns entries newest first, so grouping is a fold
    /// over the order it gave rather than a sort of our own.
    private func groups(_ entries: [HistoryEntry]) -> [DayGroup] {
        var result: [DayGroup] = []
        for entry in entries {
            let heading = Fmt.dayHeading(entry.playedAt)
            if let last = result.last, last.heading == heading {
                result[result.count - 1] = DayGroup(heading: heading, entries: last.entries + [entry])
            } else {
                result.append(DayGroup(heading: heading, entries: [entry]))
            }
        }
        return result
    }

    private func reload() async {
        let client = app.client
        let filter = filter
        let key = "tv-history:\(filter.rawValue)"
        if let cached: Pager<HistoryEntry> = app.pagers.existing(key) {
            pager = cached
            return
        }
        let next = Pager<HistoryEntry> { page in
            try await client.history(filter: filter, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }
}
