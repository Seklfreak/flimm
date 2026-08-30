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

    private static let cardWidth: CGFloat = 460

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Text(store.total > 0 ? "Comments · \(Fmt.count(store.total))" : "Comments")
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
        // Loads when the tab is first shown, and again if the video changes
        // under an open panel. Comments are the longest thing attached to a
        // video and the least often wanted; nothing is fetched until here.
        .task(id: model.videoId) {
            await store.load(videoID: model.videoId, client: model.client)
        }
    }

    @ViewBuilder
    private var content: some View {
        if store.isLoading && store.comments.isEmpty {
            message("Loading comments…")
        } else if store.failed {
            message("Comments could not be loaded.")
        } else if store.comments.isEmpty {
            message("No comments were archived with this video.")
        } else {
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: 20) {
                    ForEach(store.comments) { comment in
                        TVCommentCard(comment: comment)
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
                // Focus grows a card past its bounds; without room for that
                // the first and last are clipped by the scroller.
                .padding(.vertical, 12)
            }
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

    @Environment(\.isFocused) private var isFocused

    var body: some View {
        Button {
            // Nothing to do: the card is a card. It is a button because that
            // is how a view becomes focusable on tvOS, and focus is what makes
            // a row of them navigable at all.
        } label: {
            VStack(alignment: .leading, spacing: 8) {
                header
                Text(comment.text)
                    .font(.callout)
                    .multilineTextAlignment(.leading)
                    .lineLimit(4)
                    .frame(maxWidth: .infinity, alignment: .leading)
                if !comment.replies.isEmpty {
                    Text("\(comment.replies.count) \(comment.replies.count == 1 ? "reply" : "replies")")
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
