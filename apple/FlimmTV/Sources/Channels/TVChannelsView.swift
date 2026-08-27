import FlimmKit
import SwiftUI

/// The channel directory as a grid of avatars. Sorting is the only control:
/// the "channels in no feed" filter exists to build a feed, and feeds are not
/// built here.
struct TVChannelsView: View {
    @Environment(AppModel.self) private var app

    @State private var pager: Pager<ChannelSummary>?
    @State private var sort: ChannelSort = .name

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                HStack(alignment: .bottom) {
                    TVScreenTitle(title: "Channels")
                    Spacer(minLength: 40)
                    Picker("Sort", selection: $sort) {
                        Text("Name").tag(ChannelSort.name)
                        Text("Videos").tag(ChannelSort.videos)
                        Text("Unseen").tag(ChannelSort.unseen)
                        Text("Recent").tag(ChannelSort.lastUpload)
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
        .task(id: sort) { await reload() }
    }

    @ViewBuilder
    private var content: some View {
        if let pager {
            if let error = pager.error, pager.items.isEmpty {
                TVErrorState(message: error) { Task { await pager.reload() } }
            } else if pager.items.isEmpty && pager.hasLoaded {
                TVEmptyState(icon: "person.2", title: "No channels")
            } else {
                LazyVGrid(columns: TVGrids.tiles, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                    ForEach(pager.items) { channel in
                        TVChannelCard(channel: channel)
                            .task { await pager.loadMoreIfNeeded(after: channel) }
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

    private func reload() async {
        let client = app.client
        let sort = sort
        let key = "tv-channels:\(sort.rawValue)"
        if let cached: Pager<ChannelSummary> = app.pagers.existing(key) {
            pager = cached
            return
        }
        let next = Pager<ChannelSummary> { page in
            try await client.channels(sort: sort, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }
}
