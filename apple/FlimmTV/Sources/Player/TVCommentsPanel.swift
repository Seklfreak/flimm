import FlimmKit
import SwiftUI

/// The archived comments, as a second tab in the player's Info panel.
///
/// **Why a row and not a list.** The panel AVKit hands a custom tab is a wide,
/// short band across the top of the screen — the same constraint that shaped
/// ``TVPlayerInfoPanel`` into two columns. A vertical list of comments there
/// shows two of them and clips the third; a *horizontal* row of cards fits the
/// band exactly, and moving right through them is what a remote is good at.
///
/// It is the only place comments can live on this platform: selecting a video
/// plays it, so there is no detail screen to put them on, and the Info panel is
/// where tvOS puts everything else about what is playing.
struct TVCommentsPanel: View {
    let model: TVWatchModel

    @State private var store = CommentsStore()
    /// The thread being read, if the viewer opened one. Replies live one level
    /// down rather than on the card: a band this short cannot show a thread and
    /// the row it came from at the same time.
    @State private var openThread: VideoComment?

    private static let cardWidth: CGFloat = 460

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(title)
                .font(.title3.bold())
                .padding(.horizontal, 40)
            content
        }
        .padding(.vertical, 24)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(
            Color.black.opacity(0.35),
            in: RoundedRectangle(cornerRadius: TVPlayerInfoPanel.groundRadius, style: .continuous)
        )
        .padding(.horizontal, TVPlayerInfoPanel.groundInset)
        // Menu goes back a level rather than straight out, which is what the
        // button means everywhere else on the platform. Left unset when there
        // is no thread open, so it stays AVKit's — Menu then closes the panel.
        .onExitCommand(perform: openThread == nil ? nil : { openThread = nil })
        // Loads when the tab is first shown, and again if the video changes
        // under an open panel. Comments are the longest thing attached to a
        // video and the least often wanted; nothing is fetched until here.
        .task(id: model.videoId) {
            await store.load(videoID: model.videoId, client: model.client)
            openDebugThread()
        }
    }

    /// Debug builds can open straight into a thread, because selecting a card
    /// needs a remote and a simulator has none:
    ///
    ///     SIMCTL_CHILD_FLIMM_OPEN_COMMENT=1 xcrun simctl launch <device> …
    ///
    /// The number is the position in the row, counting from 1. A shipped app
    /// has no such door.
    private func openDebugThread() {
        #if DEBUG
        guard let raw = ProcessInfo.processInfo.environment["FLIMM_OPEN_COMMENT"],
              let index = Int(raw), index >= 1, index <= store.comments.count else {
            return
        }
        openThread = store.comments[index - 1]
        #endif
    }

    private var title: String {
        if let openThread {
            let count = openThread.replies.count
            return "\(count) \(count == 1 ? "reply" : "replies") to \(openThread.author)"
        }
        return store.total > 0 ? "Comments · \(Fmt.count(store.total))" : "Comments"
    }

    @ViewBuilder
    private var content: some View {
        if store.isLoading && store.comments.isEmpty {
            message("Loading comments…")
        } else if store.failed {
            message("Comments could not be loaded.")
        } else if store.comments.isEmpty {
            message("No comments were archived with this video.")
        } else if let thread = openThread {
            replies(of: thread)
        } else {
            threads
        }
    }

    private var threads: some View {
        ScrollView(.horizontal) {
            LazyHStack(alignment: .top, spacing: 20) {
                ForEach(store.comments) { comment in
                    TVCommentCard(comment: comment, showsFullText: false) {
                        // Only a thread with replies has anywhere to go; a
                        // card that led nowhere would be worse than one that
                        // does not react.
                        guard !comment.replies.isEmpty else { return }
                        openThread = comment
                    }
                    .frame(width: Self.cardWidth)
                }
                if store.hasMore {
                    Button {
                        Task { await store.loadMore(client: model.client) }
                    } label: {
                        Text(store.isLoading ? "Loading…" : "More comments")
                            .frame(maxWidth: .infinity, minHeight: 100)
                    }
                    .frame(width: 260)
                    .disabled(store.isLoading)
                }
            }
            .padding(.horizontal, 40)
            // Focus grows a card past its bounds; without room for that the
            // first and last are clipped by the scroller.
            .padding(.vertical, 12)
        }
    }

    /// One thread: the comment it started from, then its replies, in the same
    /// row shape the band fits. The parent is shown in full here — the card in
    /// the list truncates it, so opening a thread is also how a long comment
    /// gets read.
    private func replies(of thread: VideoComment) -> some View {
        ScrollView(.horizontal) {
            LazyHStack(alignment: .top, spacing: 20) {
                Button {
                    openThread = nil
                } label: {
                    Label("Back", systemImage: "chevron.left")
                        .frame(maxWidth: .infinity, minHeight: 100)
                }
                .frame(width: 200)

                TVCommentCard(comment: thread, showsFullText: true) {}
                    .frame(width: Self.cardWidth)

                ForEach(thread.replies) { reply in
                    TVCommentCard(comment: reply, showsFullText: true) {}
                        .frame(width: Self.cardWidth)
                }
            }
            .padding(.horizontal, 40)
            .padding(.vertical, 12)
        }
    }

    private func message(_ text: String) -> some View {
        Text(text)
            .font(.callout)
            .foregroundStyle(.secondary)
            .padding(.horizontal, 40)
            .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// One comment as a card. Focusable so the remote can move through the row,
/// and so a long comment can be read: focus is what tells tvOS which card the
/// viewer means.
private struct TVCommentCard: View {
    let comment: VideoComment
    /// A card in the list is trimmed to four lines; the same comment opened as
    /// a thread is not, because that is what opening it was for.
    let showsFullText: Bool
    let select: () -> Void

    var body: some View {
        Button(action: select) {
            VStack(alignment: .leading, spacing: 8) {
                header
                Text(comment.text)
                    .font(.callout)
                    .multilineTextAlignment(.leading)
                    .lineLimit(showsFullText ? nil : 4)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if !comment.replies.isEmpty {
                    Label(
                        "\(comment.replies.count) \(comment.replies.count == 1 ? "reply" : "replies")",
                        systemImage: "chevron.right"
                    )
                    .font(.caption.weight(.bold))
                    .foregroundStyle(.secondary)
                }
            }
            .frame(maxWidth: .infinity, minHeight: 120, alignment: .topLeading)
            .padding(16)
        }
        .buttonStyle(.card)
    }

    private var header: some View {
        HStack(spacing: 8) {
            // An initial rather than an avatar: the archive's avatar URL points
            // at Google's CDN, and loading it would tell a third party what is
            // being watched — the one thing archived comments otherwise avoid.
            Text(comment.initial)
                .font(.system(size: 15, weight: .heavy))
                .frame(width: 34, height: 34)
                .background(Palette.raised, in: Circle())
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(comment.author)
                        .font(.caption.weight(.bold))
                        .lineLimit(1)
                    if comment.fromUploader {
                        Text("Uploader")
                            .font(.caption2.weight(.bold))
                            .padding(.horizontal, 7)
                            .padding(.vertical, 2)
                            .background(Palette.accent, in: Capsule())
                    }
                    if comment.hearted {
                        Image(systemName: "heart.fill")
                            .font(.caption2)
                            .foregroundStyle(.pink)
                    }
                }
                HStack(spacing: 6) {
                    Text(comment.when { Fmt.relativeDay($0) })
                    if comment.likes > 0 {
                        Text("· \(Fmt.count(comment.likes)) likes")
                    }
                }
                .font(.caption2)
                .foregroundStyle(.secondary)
            }
            Spacer(minLength: 0)
        }
    }
}
