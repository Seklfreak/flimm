import FlimmKit
import SwiftUI

/// The companion: what is playing on the television, and everything about it
/// that is easier to read in your hand than across a room.
///
/// The transport is the small half. The reason this screen exists is the other
/// half — the description and the archived comments, which on a television are
/// a wall of text nobody reads at two metres, and here are simply a page.
struct RemoteScreen: View {
    @Environment(AppModel.self) private var app
    @Environment(RemoteControl.self) private var remote
    @Environment(\.dismiss) private var dismiss

    @State private var details = RemoteDetails()
    @State private var scrubPreview = ScrubPreviewState()
    /// Set while a finger is on the scrubber. The projected clock keeps running
    /// underneath, and this is what stops it fighting the drag.
    @State private var scrubbing: Double?

    /// The stills to load, or nil while there is nothing to load them for.
    ///
    /// Asking is what makes the server derive them, and the television never
    /// asks — tvOS scrubs without pictures. So this is the first request for
    /// most videos, and it is deliberately tied to the companion being open:
    /// somebody holding the phone is the person about to drag the bar. The
    /// result is a cached variant, so it is a cost paid once per video.
    private var scrubPreviewPath: String? {
        details.video?.scrubPreviewPath
    }

    var body: some View {
        NavigationStack {
            Group {
                if let session = remote.current {
                    content(session)
                } else {
                    stopped
                }
            }
            .navigationTitle(remote.current?.device ?? "Remote")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        // Keyed on the video rather than the session: stepping to the next
        // video is the same session, and the description has to follow it.
        .task(id: remote.current?.videoId) {
            guard let id = remote.current?.videoId else { return }
            await details.load(videoID: id, client: app.client)
        }
        // Separate from the details load: the path only exists once that has
        // come back, and the key is nil until then, so this runs on the
        // transition and once per video.
        .task(id: scrubPreviewPath) {
            guard let path = scrubPreviewPath else { return }
            await scrubPreview.load(path: path, client: app.client)
        }
    }

    private func content(_ session: RemoteSession) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                header(session)
                transport(session)
                if !details.chapters.isEmpty {
                    chapters(session)
                }
                if let video = details.video {
                    description(video)
                    CommentsSection(videoID: video.id, duration: video.duration) { seconds in
                        Task { await remote.seek(to: seconds) }
                    }
                } else if details.isLoading {
                    ProgressView().frame(maxWidth: .infinity)
                } else if details.failed {
                    Text("The rest of this video's details could not be loaded.")
                        .font(.subheadline)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(16)
        }
        .background(Palette.background)
    }

    /// What happens when the television is switched off with the companion
    /// open. Saying so beats a screen of stale controls that steer nothing.
    private var stopped: some View {
        VStack(spacing: 12) {
            Image(systemName: "tv.slash")
                .font(.largeTitle)
                .foregroundStyle(.secondary)
            Text("Nothing is playing any more.")
                .font(.headline)
            Button("Done") { dismiss() }
                .buttonStyle(.borderedProminent)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Palette.background)
    }

    // MARK: - Now playing

    private func header(_ session: RemoteSession) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            MediaImage(path: session.thumbUrl)
                .aspectRatio(16 / 9, contentMode: .fit)
                .frame(maxWidth: .infinity)
                .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
            Text(session.title)
                .font(.title3.bold())
                .fixedSize(horizontal: false, vertical: true)
            Text(session.channelName)
                .font(.subheadline)
                .foregroundStyle(.secondary)
        }
    }

    // MARK: - Transport

    private func transport(_ session: RemoteSession) -> some View {
        // Once a second is enough for a clock measured in seconds, and it is a
        // redraw of the scrubber rather than of the page.
        TimelineView(.periodic(from: .now, by: 1)) { context in
            let live = RemoteClock.position(of: session, receivedAt: remote.receivedAt, now: context.date)
            let shown = scrubbing ?? live
            VStack(spacing: 8) {
                scrubber(session, at: shown)
                times(session, at: shown)
                buttons(session)
            }
        }
    }

    /// The player's own scrubber, steering the television instead of a local
    /// `AVPlayer`: the same preview stills, chapter ticks and SponsorBlock
    /// tints, because a bar you drag from across the room is the one that most
    /// needs to say what it is about to land on.
    private func scrubber(_ session: RemoteSession, at position: Double) -> some View {
        ScrubberView(
            currentTime: position,
            duration: session.duration,
            chapters: details.chapters,
            sponsors: details.video?.sponsorblock ?? [],
            preview: scrubPreview.tiles,
            previewSheet: scrubPreview.sheet,
            style: .onSurface,
            // Held locally while the finger is down. The seek goes on release,
            // not on every value: dragging across a video would otherwise be
            // dozens of commands, each of which the television would honour.
            onScrub: { scrubbing = $0 },
            onCommit: { target in
                scrubbing = nil
                Task { await remote.seek(to: target) }
            }
        )
        .disabled(session.duration <= 0)
    }

    private func times(_ session: RemoteSession, at position: Double) -> some View {
        HStack {
            Text(Fmt.duration(position))
            Spacer()
            if let chapter = details.chapter(at: position) {
                Text(chapter.title)
                    .lineLimit(1)
                    .foregroundStyle(.secondary)
                Spacer()
            }
            Text("−" + Fmt.duration(max(0, session.duration - position)))
        }
        .font(.caption.monospacedDigit())
        .foregroundStyle(.secondary)
    }

    private func buttons(_ session: RemoteSession) -> some View {
        HStack(spacing: 28) {
            step(
                "backward.end.fill",
                label: "Previous video",
                enabled: session.canPrevious
            ) { await remote.goPrevious() }
            step("gobackward.10", label: "Back 10 seconds") { await remote.skip(-10) }
            Button {
                Task { await remote.togglePlayPause() }
            } label: {
                Image(systemName: session.paused ? "play.circle.fill" : "pause.circle.fill")
                    .font(.system(size: 56))
                    .symbolRenderingMode(.hierarchical)
                    .foregroundStyle(Palette.accent)
            }
            .buttonStyle(.plain)
            .accessibilityLabel(session.paused ? "Play" : "Pause")
            step("goforward.30", label: "Forward 30 seconds") { await remote.skip(30) }
            step("forward.end.fill", label: "Next video", enabled: session.canNext) { await remote.goNext() }
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 4)
    }

    private func step(
        _ icon: String,
        label: String,
        enabled: Bool = true,
        action: @escaping () async -> Void
    ) -> some View {
        Button { Task { await action() } } label: {
            Image(systemName: icon)
                .font(.title2)
                .frame(width: 44, height: 44)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(!enabled)
        .foregroundStyle(enabled ? Color.primary : Color.secondary.opacity(0.4))
        .accessibilityLabel(label)
    }

    // MARK: - The video itself

    /// Chapters are the one part of the companion that is also a control: the
    /// list a television shows in a transport bar is here a set of destinations,
    /// and tapping one moves the television.
    private func chapters(_ session: RemoteSession) -> some View {
        TimelineView(.periodic(from: .now, by: 1)) { context in
            let position = RemoteClock.position(of: session, receivedAt: remote.receivedAt, now: context.date)
            let current = details.chapter(at: position)
            VStack(alignment: .leading, spacing: 8) {
                Text("Chapters")
                    .font(.headline)
                ForEach(details.chapters) { chapter in
                    Button {
                        Task { await remote.seek(to: chapter.start) }
                    } label: {
                        HStack(spacing: 10) {
                            Text(Fmt.duration(chapter.start))
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                            Text(chapter.title)
                                .font(.subheadline)
                                .lineLimit(2)
                                .multilineTextAlignment(.leading)
                            Spacer(minLength: 0)
                        }
                        .padding(.vertical, 6)
                        .padding(.horizontal, 10)
                        .background(
                            chapter == current ? Palette.raised : .clear,
                            in: RoundedRectangle(cornerRadius: 8, style: .continuous)
                        )
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                }
            }
        }
    }

    private func description(_ video: Video) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            if !video.description.isEmpty {
                Text("Description")
                    .font(.headline)
                // A timestamp here seeks the television, like the chapter
                // list above it does — the same one-way steering.
                RichTextView(
                    text: video.description,
                    duration: video.duration,
                    onSeek: { seconds in Task { await remote.seek(to: seconds) } },
                    style: .footnote,
                    color: .secondaryLabel
                )
                .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(12)
                    .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
            }
        }
    }
}
