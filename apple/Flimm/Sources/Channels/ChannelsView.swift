import FlimmKit
import SwiftUI

/// The channel directory: search, sort, and a filter for channels that are in
/// no feed — the ones a new feed is usually made of.
struct ChannelsView: View {
    @Environment(AppModel.self) private var app
    @Environment(NavigationModel.self) private var nav
    @State private var pager: Pager<ChannelSummary>?
    @State private var searchText = ""
    @State private var sort: ChannelSort = .name
    @State private var unfeededOnly = false
    @State private var showAddChannel = false
    @State private var newChannel = ""
    /// The subscribe in flight: the request holds while TubeArchivist
    /// resolves and creates the channel, and the screen says so.
    @State private var adding = false
    /// TubeArchivist was still resolving when the server stopped waiting.
    @State private var addPending = false
    @State private var addError: String?

    /// The pinned section leads the directory, but never a search or filter:
    /// those are questions about the whole archive, not the pins.
    private var showsPinned: Bool {
        !app.pinnedChannels.isEmpty && searchText.isEmpty && !unfeededOnly
    }

    var body: some View {
        List {
            if showsPinned {
                Section("Pinned") {
                    ForEach(app.pinnedChannels) { channel in
                        NavigationLink(value: Route.channel(channel.id)) {
                            ChannelRow(channel: channel)
                        }
                    }
                }
            }
            if let pager {
                if let error = pager.error, pager.items.isEmpty {
                    ErrorState(message: error) { Task { await pager.reload() } }
                        .listRowSeparator(.hidden)
                } else if pager.items.isEmpty && pager.hasLoaded {
                    EmptyState(
                        icon: "person.2",
                        title: unfeededOnly ? "Every channel is in a feed" : "No channels",
                        message: unfeededOnly ? nil : "Nothing matched that search."
                    )
                    .listRowSeparator(.hidden)
                } else {
                    ForEach(pager.items) { channel in
                        NavigationLink(value: Route.channel(channel.id)) {
                            ChannelRow(channel: channel)
                        }
                        .task { await pager.loadMoreIfNeeded(after: channel) }
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
        .navigationTitle("Channels")
        .onAppear { Analytics.screen(.channels) }
        // Debug builds can start the subscribe at launch
        // (`FLIMM_SUBSCRIBE_CHANNEL=@handle`, with `FLIMM_OPEN_TAB=channels`):
        // the wait, the channel it opens and the failure alert are all
        // behind a menu and a text field a simulator cannot tap. A shipped
        // app has no such door.
        .task {
            #if DEBUG
            if let value = ProcessInfo.processInfo.environment["FLIMM_SUBSCRIBE_CHANNEL"], !value.isEmpty {
                await subscribe(value)
            }
            #endif
        }
        .searchable(text: $searchText, isPresented: nav.searchPresented(for: .channels), prompt: "Search channels")
        .alert("Add channel", isPresented: $showAddChannel) {
            TextField("URL, @handle or UC… id", text: $newChannel)
                .autocorrectionDisabled()
                .textInputAutocapitalization(.never)
            Button("Subscribe") {
                let value = newChannel.trimmingCharacters(in: .whitespaces)
                newChannel = ""
                guard !value.isEmpty else { return }
                Task { await subscribe(value) }
            }
            Button("Cancel", role: .cancel) { newChannel = "" }
        } message: {
            Text("The archive resolves the channel and starts downloading it; this opens the channel once it is indexed.")
        }
        .alert("Still resolving", isPresented: $addPending) {
            Button("OK") {}
        } message: {
            Text("TubeArchivist is still at it. The channel appears in the directory once that lands.")
        }
        .alert("Could not subscribe", isPresented: Binding(get: { addError != nil }, set: { if !$0 { addError = nil } })) {
            Button("OK") {}
        } message: {
            Text(addError ?? "")
        }
        .overlay {
            if adding {
                ProgressView("Waiting for TubeArchivist to resolve and index the channel…")
                    .multilineTextAlignment(.center)
                    .padding(24)
                    .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
                    .padding(40)
            }
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    if app.me?.isAdmin == true {
                        Button {
                            showAddChannel = true
                        } label: {
                            Label("Add channel…", systemImage: "plus")
                        }
                        Divider()
                    }
                    Picker("Sort", selection: $sort) {
                        Text("Name").tag(ChannelSort.name)
                        Text("Most videos").tag(ChannelSort.videos)
                        Text("Most unseen").tag(ChannelSort.unseen)
                        Text("Last upload").tag(ChannelSort.lastUpload)
                    }
                    Divider()
                    Toggle("Only channels in no feed", isOn: $unfeededOnly)
                } label: {
                    Image(systemName: "arrow.up.arrow.down.circle")
                }
            }
        }
        .task(id: queryKey) { await reload() }
    }

    private var queryKey: String { "\(searchText)|\(sort.rawValue)|\(unfeededOnly)|\(app.listGeneration)" }

    private func reload() async {
        let client = app.client
        let text = searchText.trimmingCharacters(in: .whitespaces)
        let sort = sort
        let unfeeded = unfeededOnly
        let key = "channels:\(text)|\(sort.rawValue)|\(unfeeded)"
        if let cached: Pager<ChannelSummary> = app.pagers.existing(key) {
            pager = cached
            return
        }
        if !text.isEmpty {
            try? await Task.sleep(for: .milliseconds(250))
            if Task.isCancelled { return }
        }
        let next = Pager<ChannelSummary> { page in
            try await client.channels(query: text.isEmpty ? nil : text, sort: sort, unfeeded: unfeeded, page: page)
        }
        app.pagers.insert(next, for: key)
        pager = next
        await next.reload()
    }

    /// Holds on the request until TubeArchivist has the channel, then opens
    /// it; a failure on the archive's side is shown with its reason rather
    /// than swallowed.
    private func subscribe(_ value: String) async {
        adding = true
        defer { adding = false }
        do {
            let result = try await app.client.subscribeNewChannel(value)
            await pager?.reload()
            if let channel = result.channel {
                nav.push(.channel(channel.id))
            } else {
                addPending = true
            }
        } catch {
            addError = error.localizedDescription
        }
    }

}

struct ChannelRow: View {
    let channel: ChannelSummary

    var body: some View {
        HStack(spacing: 12) {
            ChannelAvatar(path: channel.thumbUrl, name: channel.name, size: 44)
            VStack(alignment: .leading, spacing: 2) {
                Text(channel.name)
                    .font(.subheadline.weight(.bold))
                    .lineLimit(1)
                Text(subtitle)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
            UnseenBadge(count: channel.unseenCount)
        }
        .padding(.vertical, 2)
    }

    private var subtitle: String {
        let feeds = channel.feeds.filter { $0.id != Feed.everythingID }.map(\.name)
        var parts = [Fmt.plural(channel.videoCount, "video")]
        if let last = channel.lastUpload { parts.append("updated \(Fmt.relativeDay(last))") }
        parts.append(feeds.isEmpty ? "not in a feed" : "in \(feeds.joined(separator: ", "))")
        return parts.joined(separator: " · ")
    }
}
