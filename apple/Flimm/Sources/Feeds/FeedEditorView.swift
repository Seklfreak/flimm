import FlimmKit
import SwiftUI

/// One form for *New feed* and *Edit feed*, as on the web: name, a channel
/// picker and the feed options. Deleting a feed never touches channels or
/// videos — only the feed itself.
struct FeedEditorView: View {
    /// `nil` creates a new feed.
    let feedId: String?

    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss

    @State private var name = ""
    @State private var channelIds: Set<String> = []
    @State private var sort: FeedSort = .newest
    @State private var hideSeen = true
    @State private var includeShorts = false
    @State private var subtitlesOnly = false
    @State private var pinned = false
    @State private var isSaving = false
    @State private var error: String?
    @State private var confirmDelete = false
    /// The form is filled from the feed exactly once. `.task` runs again every
    /// time this screen reappears — including on the way back from the channel
    /// picker — and refilling there would throw away the selection the viewer
    /// just made (and any name they had typed) before Save ever saw it.
    @State private var hasLoaded = false

    private var isNew: Bool { feedId == nil }

    var body: some View {
        Form {
            Section("Name") {
                TextField("Home", text: $name)
                    .autocorrectionDisabled()
            }
            Section("Channels") {
                NavigationLink {
                    ChannelPickerView(selection: $channelIds)
                } label: {
                    LabeledContent("Channels", value: Fmt.plural(channelIds.count, "channel"))
                }
                if channelIds.isEmpty {
                    Text("A feed with no channels shows nothing. Pick at least one.")
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }
            }
            Section("Options") {
                Picker("Sort", selection: $sort) {
                    ForEach(FeedSort.allCases, id: \.self) { option in
                        Text(option.label).tag(option)
                    }
                }
                Toggle("Hide seen by default", isOn: $hideSeen)
                Toggle("Include Shorts", isOn: $includeShorts)
                Toggle("Only videos with subtitles", isOn: $subtitlesOnly)
                Toggle("Pin to top", isOn: $pinned)
            }
            if let error {
                Section {
                    Text(error)
                        .font(.footnote)
                        .foregroundStyle(Palette.danger)
                }
            }
            if !isNew {
                Section {
                    Button("Delete feed", role: .destructive) { confirmDelete = true }
                }
            }
        }
        .navigationTitle(isNew ? "New feed" : "Edit feed")
        .onAppear { Analytics.screen(.feedEditor) }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .confirmationAction) {
                Button("Save") { Task { await save() } }
                    .disabled(isSaving || name.trimmingCharacters(in: .whitespaces).isEmpty)
            }
        }
        .confirmationDialog("Delete this feed?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete feed", role: .destructive) { Task { await delete() } }
        } message: {
            Text("The channels and their videos are untouched.")
        }
        .task { load() }
    }

    private func load() {
        guard !hasLoaded, let feedId, let feed = app.feeds.first(where: { $0.id == feedId }) else { return }
        hasLoaded = true
        name = feed.name
        channelIds = Set(feed.channelIds)
        sort = feed.sort
        hideSeen = feed.hideSeen
        includeShorts = feed.includeShorts
        subtitlesOnly = feed.subtitlesOnly
        pinned = feed.pinned
    }

    private func save() async {
        isSaving = true
        defer { isSaving = false }
        let input = FeedInput(
            name: name.trimmingCharacters(in: .whitespaces),
            channelIds: Array(channelIds),
            sort: sort,
            hideSeen: hideSeen,
            includeShorts: includeShorts,
            subtitlesOnly: subtitlesOnly,
            pinned: pinned
        )
        do {
            if let feedId {
                _ = try await app.client.updateFeed(feedId, input)
            } else {
                _ = try await app.client.createFeed(input)
                Analytics.feedCreated()
            }
            // The feed's channels decide what its list contains, so the cached
            // pages of it are wrong now (see ``PagerStore``).
            app.pagers.removeAll()
            await app.refreshFeeds()
            dismiss()
        } catch {
            self.error = AppModel.message(for: error)
        }
    }

    private func delete() async {
        guard let feedId else { return }
        do {
            try await app.client.deleteFeed(feedId)
            app.pagers.removeAll()
            await app.refreshFeeds()
            dismiss()
        } catch {
            self.error = AppModel.message(for: error)
        }
    }
}

extension FeedSort {
    var label: String {
        switch self {
        case .newest: "Newest first"
        case .oldest: "Oldest first"
        case .shortest: "Shortest first"
        case .longest: "Longest first"
        }
    }
}

/// Reorder, delete and create feeds. Order is `position`, which the server
/// owns — `POST /feeds/reorder` sends the new sequence.
struct FeedManagerView: View {
    @Environment(AppModel.self) private var app
    @State private var order: [Feed] = []

    var body: some View {
        List {
            Section {
                ForEach(order) { feed in
                    NavigationLink(value: Route.feedEditor(feedId: feed.id)) {
                        HStack {
                            if feed.pinned {
                                Image(systemName: "pin.fill")
                                    .font(.caption)
                                    .foregroundStyle(Palette.accent)
                            }
                            Text(feed.name)
                            Spacer()
                            Text(Fmt.plural(feed.channelCount, "channel"))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                    .deleteDisabled(feed.isEverything)
                }
                .onMove { source, destination in
                    order.move(fromOffsets: source, toOffset: destination)
                    Task { await persistOrder() }
                }
                .onDelete { offsets in
                    Task { await delete(at: offsets) }
                }
            } footer: {
                Text("“Everything” is built in: it covers every channel and is always last.")
            }
        }
        .environment(\.editMode, .constant(.active))
        .navigationTitle("Feeds")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                NavigationLink(value: Route.feedEditor(feedId: nil)) {
                    Image(systemName: "plus")
                }
            }
        }
        .task { order = app.feeds }
        .onChange(of: app.feeds) { _, feeds in order = feeds }
    }

    private func persistOrder() async {
        try? await app.client.reorderFeeds(order.map(\.id))
        await app.refreshFeeds()
    }

    private func delete(at offsets: IndexSet) async {
        let doomed = offsets.map { order[$0] }.filter { !$0.isEverything }
        for feed in doomed {
            try? await app.client.deleteFeed(feed.id)
        }
        app.pagers.removeAll()
        await app.refreshFeeds()
    }
}
