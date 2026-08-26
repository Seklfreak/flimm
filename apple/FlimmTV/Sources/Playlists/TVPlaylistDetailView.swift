import FlimmKit
import SwiftUI

/// One playlist, in its own order.
///
/// A **music** playlist is the reason audio-only exists: it records no watch
/// state at all and every route into it carries `audio=1`, so playback uses the
/// derived AAC rendition and the screen shows artwork instead of video.
struct TVPlaylistDetailView: View {
    let playlistId: String

    @Environment(AppModel.self) private var app
    @Environment(TVPlayerCoordinator.self) private var player

    @State private var playlist: Playlist?
    @State private var error: String?

    private var summary: PlaylistSummary? { playlist?.summary }
    private var isMusic: Bool { summary?.music == true }

    private var context: PlaybackContext {
        .playlist(playlistId, audioOnly: isMusic)
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 30) {
                header
                content
            }
            .padding(.horizontal, TVMetrics.margin)
            .padding(.bottom, TVMetrics.margin)
        }
        .task { await load() }
    }

    private var header: some View {
        HStack(alignment: .center, spacing: 30) {
            MediaImage(path: summary?.thumbUrl)
                .aspectRatio(16 / 9, contentMode: .fill)
                .frame(width: 400, height: 225)
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            VStack(alignment: .leading, spacing: 10) {
                Text(playlist?.name ?? "Playlist")
                    .font(.system(size: 48, weight: .bold))
                    .lineLimit(2)
                Text(meta)
                    .font(.title3)
                    .foregroundStyle(.secondary)
                HStack(spacing: 18) {
                    Button {
                        Task { await play() }
                    } label: {
                        Label(playLabel, systemImage: isMusic ? "music.note" : "play.fill")
                    }
                    .disabled(playlist?.items.isEmpty != false)

                    Button {
                        Task { await shuffle() }
                    } label: {
                        Label("Shuffle", systemImage: "shuffle")
                    }
                    .disabled(playlist?.items.isEmpty != false)
                }
                .padding(.top, 8)
            }
            Spacer(minLength: 0)
        }
        .padding(.top, 20)
    }

    private var playLabel: String {
        guard !isMusic, summary?.resumeVideoId != nil else { return "Play" }
        return "Resume"
    }

    private var meta: String {
        guard let summary else { return "" }
        var parts = [Fmt.plural(summary.videoCount, "video")]
        if summary.totalDuration > 0 { parts.append(Fmt.durationLong(summary.totalDuration)) }
        if isMusic {
            parts.append("music · audio only, no watch state")
        } else {
            let remaining = Fmt.remainingUnseen(videoCount: summary.videoCount, seenCount: summary.seenCount)
            if remaining > 0 { parts.append("\(Fmt.count(remaining)) unseen") }
        }
        if let channel = summary.channel { parts.append(channel.name) }
        return parts.joined(separator: " · ")
    }

    @ViewBuilder
    private var content: some View {
        if let error {
            TVErrorState(message: error) { Task { await load() } }
        } else if let playlist {
            if playlist.items.isEmpty {
                TVEmptyState(icon: "list.and.film", title: "This playlist is empty")
            } else {
                LazyVGrid(columns: TVGrids.videos, alignment: .leading, spacing: TVMetrics.gridSpacing) {
                    ForEach(playlist.items) { item in
                        TVVideoCard(video: item.video, context: context)
                    }
                }
            }
        } else {
            TVLoadingState()
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

    /// Resume is the server's answer: `resume_video_id` is the first
    /// in-progress video, else the first unseen one. A music playlist reports
    /// none, so it starts at the top.
    private func play() async {
        guard let playlist else { return }
        let start = summary?.resumeVideoId ?? playlist.items.first?.video.id
        guard let start else { return }
        player.play(start, context: context)
    }

    private func shuffle() async {
        guard let anchor = playlist?.items.first?.video.id else { return }
        await TVShuffle.start(
            from: anchor,
            source: .playlist(playlistId),
            audioOnly: isMusic,
            client: app.client,
            player: player
        )
    }
}
