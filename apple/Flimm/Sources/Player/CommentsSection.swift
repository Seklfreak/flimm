import FlimmKit
import SwiftUI

/// The archived comments, under the video's description on every screen size.
///
/// Open from the start: they belong under the description rather than behind a
/// button that has to be found, and the first page loads with the video. The
/// section can still be collapsed, and closing it is remembered for as long as
/// the app is running — someone who does not want comments should not have to
/// close them on every video. The web client behaves the same way.
struct CommentsSection: View {
    let videoID: String

    @Environment(AppModel.self) private var app
    @State private var store = CommentsStore()
    @State private var isOpen = CommentsSection.openByDefault

    /// Whether the section opens, for the rest of this launch; see above.
    @MainActor private static var openByDefault = true

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Button {
                withAnimation(.easeOut(duration: 0.15)) { isOpen.toggle() }
                CommentsSection.openByDefault = isOpen
            } label: {
                HStack {
                    Text(store.total > 0 ? "Comments · \(Fmt.count(store.total))" : "Comments")
                        .font(.headline)
                    Spacer(minLength: 8)
                    Image(systemName: "chevron.down")
                        .font(.system(size: 13, weight: .bold))
                        .foregroundStyle(.secondary)
                        .rotationEffect(.degrees(isOpen ? 180 : 0))
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(isOpen ? "Hide comments" : "Show comments")

            if isOpen {
                content
            }
        }
        // `.task(id:)` rather than a button action: it re-runs when the video
        // changes under a section that is already open, and cancels itself
        // when the screen goes away.
        .task(id: TaskKey(videoID: videoID, isOpen: isOpen)) {
            guard isOpen else { return }
            await store.load(videoID: videoID, client: app.client)
        }
    }

    private struct TaskKey: Hashable {
        let videoID: String
        let isOpen: Bool
    }

    @ViewBuilder
    private var content: some View {
        if store.isLoading && store.comments.isEmpty {
            ProgressView().frame(maxWidth: .infinity, alignment: .leading)
        } else if store.failed {
            HStack(spacing: 12) {
                Text("Comments could not be loaded.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                Button("Try again") {
                    Task { await store.load(videoID: videoID, client: app.client) }
                }
                .font(.subheadline.weight(.semibold))
            }
        } else if store.comments.isEmpty {
            Text("No comments were archived with this video.")
                .font(.subheadline)
                .foregroundStyle(.secondary)
        } else {
            ForEach(store.comments) { comment in
                CommentThreadView(comment: comment)
            }
            if store.hasMore {
                Button {
                    Task { await store.loadMore(client: app.client) }
                } label: {
                    if store.isLoading {
                        ProgressView()
                    } else {
                        Text("More comments")
                    }
                }
                .font(.subheadline.weight(.semibold))
                .disabled(store.isLoading)
            }
        }
    }
}

/// One comment and its replies, folded away behind their count — a long thread
/// under every comment is what makes a comment list unreadable.
private struct CommentThreadView: View {
    let comment: VideoComment

    @State private var showReplies = false

    private var repliesLabel: String {
        let count = comment.replies.count
        return "\(showReplies ? "Hide" : "Show") \(count) \(count == 1 ? "reply" : "replies")"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            CommentBodyView(comment: comment)
            if !comment.replies.isEmpty {
                VStack(alignment: .leading, spacing: 10) {
                    Button {
                        withAnimation(.easeOut(duration: 0.15)) { showReplies.toggle() }
                    } label: {
                        Text(repliesLabel)
                            .font(.caption.weight(.bold))
                    }
                    if showReplies {
                        ForEach(comment.replies) { reply in
                            CommentBodyView(comment: reply)
                        }
                    }
                }
                .padding(.leading, 44)
            }
        }
    }
}

private struct CommentBodyView: View {
    let comment: VideoComment

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            // An initial, not an avatar. The archive's avatar URL points at
            // Google's CDN, and loading it would tell a third party which
            // videos are being watched — which is the one thing showing
            // archived comments otherwise avoids.
            Text(comment.initial)
                .font(.system(size: 13, weight: .heavy))
                .foregroundStyle(.secondary)
                .frame(width: 32, height: 32)
                .background(Palette.raised, in: Circle())
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 6) {
                    Text(comment.author)
                        .font(.caption.weight(.bold))
                        .foregroundStyle(comment.fromUploader ? Color.white : .primary)
                        .padding(.horizontal, comment.fromUploader ? 8 : 0)
                        .padding(.vertical, comment.fromUploader ? 2 : 0)
                        .background(comment.fromUploader ? Palette.accent : .clear, in: Capsule())
                    Text(comment.when { Fmt.relativeDay($0) })
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    if comment.likes > 0 {
                        Text("· \(Fmt.count(comment.likes)) likes")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    if comment.hearted {
                        Image(systemName: "heart.fill")
                            .font(.caption2)
                            .foregroundStyle(.pink)
                            .accessibilityLabel("Hearted by the uploader")
                    }
                }
                Text(comment.text)
                    .font(.subheadline)
                    .textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
    }
}
