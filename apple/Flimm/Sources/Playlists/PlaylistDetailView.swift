import FlimmKit
import SwiftUI

/// A playlist and its items, with the two per-user switches the API exposes:
/// **pinned** (sidebar/first-class placement) and **music** (audio-only, and no
/// watch state recorded at all).
struct PlaylistDetailView: View {
    let playlistId: String

    @Environment(AppModel.self) private var app
    @Environment(PlayerCoordinator.self) private var player

    @State private var playlist: Playlist?
    @State private var error: String?
    @State private var isBusy = false

    private var summary: PlaylistSummary? { playlist?.summary }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if let playlist {
                    header(playlist.summary)
                    LazyVStack(alignment: .leading, spacing: 14) {
                        ForEach(playlist.items) { item in
                            HStack(alignment: .top, spacing: 10) {
                                Text("\(item.position + 1)")
                                    .font(.caption.monospacedDigit())
                                    .foregroundStyle(.secondary)
                                    .frame(width: 22, alignment: .trailing)
                                    .padding(.top, 6)
                                VideoRow(video: item.video, context: context, onDismissChange: updateVideo)
                            }
                        }
                    }
                    .padding(.horizontal, 16)
                    if playlist.items.isEmpty {
                        EmptyState(icon: "list.and.film", title: "Empty playlist")
                    }
                } else if let error {
                    ErrorState(message: error) { Task { await load() } }
                } else {
                    LoadingState()
                }
            }
            .padding(.vertical, 8)
        }
        .background(Palette.background)
        .refreshable { await load() }
        .navigationTitle(summary?.name ?? "Playlist")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { toolbar }
        .task { await load() }
    }

    /// Playback from a music playlist is audio-only and carries `audio=1`, so
    /// the server knows not to record watch state for it.
    private var context: PlaybackContext {
        PlaybackContext.playlist(playlistId, audioOnly: summary?.music ?? false)
    }

    private func header(_ summary: PlaylistSummary) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                MediaImage(path: summary.thumbUrl)
                    .frame(width: 128, height: 72)
                    .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
                VStack(alignment: .leading, spacing: 4) {
                    Text(summary.name)
                        .font(.title3.bold())
                        .lineLimit(3)
                    if let channel = summary.channel {
                        NavigationLink(value: Route.channel(channel.id)) {
                            Text(channel.name)
                                .font(.footnote.weight(.semibold))
                        }
                    }
                    Text(meta(summary))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer(minLength: 0)
            }
            HStack(spacing: 10) {
                Button {
                    startPlaying(summary)
                } label: {
                    Label(summary.resumeVideoId != nil ? "Resume" : "Play", systemImage: "play.fill")
                        .font(.subheadline.weight(.semibold))
                }
                .buttonStyle(.borderedProminent)
                .disabled(playlist?.items.isEmpty != false)

                Button {
                    Task { await shuffle() }
                } label: {
                    Label("Shuffle", systemImage: "shuffle")
                        .font(.subheadline.weight(.semibold))
                }
                .buttonStyle(.bordered)
                .disabled(playlist?.items.isEmpty != false)
            }
            if !summary.music && summary.progress > 0 {
                ProgressBar(value: summary.progress)
            }
        }
        .padding(.horizontal, 16)
    }

    private func meta(_ summary: PlaylistSummary) -> String {
        var parts = [Fmt.plural(summary.videoCount, "video")]
        if summary.totalDuration > 0 { parts.append(Fmt.durationLong(summary.totalDuration)) }
        if summary.music {
            parts.append("music")
        } else {
            let remaining = Fmt.remainingUnseen(videoCount: summary.videoCount, seenCount: summary.seenCount)
            if remaining > 0 { parts.append("\(Fmt.count(remaining)) unseen") }
        }
        return parts.joined(separator: " · ")
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarTrailing) {
            Menu {
                Toggle("Pinned", isOn: Binding(
                    get: { summary?.pinned ?? false },
                    set: { value in Task { await setPinned(value) } }
                ))
                Toggle("Music (audio only)", isOn: Binding(
                    get: { summary?.music ?? false },
                    set: { value in Task { await setMusic(value) } }
                ))
            } label: {
                Image(systemName: "ellipsis.circle")
            }
            .disabled(isBusy || playlist == nil)
        }
    }

    // MARK: - Actions

    private func load() async {
        do {
            playlist = try await app.client.playlist(playlistId)
            error = nil
        } catch {
            self.error = AppModel.message(for: error)
        }
    }

    private func startPlaying(_ summary: PlaylistSummary) {
        let target = summary.resumeVideoId ?? playlist?.items.first?.video.id
        guard let target else { return }
        player.play(target, context: context)
    }

    private func shuffle() async {
        guard let anchor = playlist?.items.first?.video.id else { return }
        let seeded = PlaybackContext(
            source: .playlist(playlistId),
            shuffleSeed: PlaybackContext.newShuffleSeed(),
            audioOnly: summary?.music ?? false
        )
        guard let nav = try? await app.client.nav(anchor, context: seeded) else { return }
        player.play(nav.first?.id ?? anchor, context: seeded)
    }

    private func setPinned(_ value: Bool) async {
        isBusy = true
        defer { isBusy = false }
        try? await app.client.setPlaylistPinned(playlistId, pinned: value)
        await app.refreshPinnedPlaylists()
        await load()
    }

    private func setMusic(_ value: Bool) async {
        isBusy = true
        defer { isBusy = false }
        try? await app.client.setPlaylistMusic(playlistId, music: value)
        await load()
    }

    /// A playlist keeps listing a video after it is dismissed — the contract
    /// makes an exception for feeds only — so this patches the row's own
    /// state in place rather than removing it.
    private func updateVideo(_ updated: VideoSummary) {
        guard let current = playlist,
              let index = current.items.firstIndex(where: { $0.video.id == updated.id }) else { return }
        var items = current.items
        items[index] = PlaylistItem(position: items[index].position, video: updated)
        playlist = Playlist(summary: current.summary, items: items)
        Task { await app.videoListStateChanged() }
    }
}
