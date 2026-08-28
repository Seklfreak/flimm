import FlimmKit
import SwiftUI

/// The full-bleed player screen, and the three states that are not "playing":
/// still loading, a video whose codec this device cannot decode, and a request
/// that failed outright.
struct TVWatchView: View {
    @Environment(TVPlayerCoordinator.self) private var player
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        ZStack {
            Color.black.ignoresSafeArea()
            if let model = player.model {
                content(model)
            } else {
                TVLoadingState()
            }
        }
        .onAppear { Analytics.screen(.watch) }
    }

    @ViewBuilder
    private func content(_ model: TVWatchModel) -> some View {
        if let issue = model.codecIssue {
            codecRefusal(issue, model: model)
        } else if model.audioUnavailable {
            problem(
                icon: "waveform.slash",
                title: "No audio-only rendition",
                message: """
                This server hasn't got an AAC audio rendition for this video. \
                Update the server, or play the video instead.
                """,
                actionTitle: "Play video",
                action: { Task { await model.toggleAudioOnly() } }
            )
        } else if let error = model.loadError {
            problem(icon: "exclamationmark.triangle", title: "Couldn't play this", message: error)
        } else {
            TVPlayerViewController(model: model)
                .ignoresSafeArea()
        }
    }

    /// The codec gate, said out loud. A named codec is something a viewer can
    /// act on; a player that never starts is not.
    private func codecRefusal(_ issue: CodecGate.Issue, model: TVWatchModel) -> some View {
        problem(
            icon: "film.stack",
            title: "This video can't play here",
            message: issue.message + (issue.audioAvailable ? " You can still listen to it." : ""),
            actionTitle: issue.audioAvailable ? "Listen instead" : nil,
            action: issue.audioAvailable ? { Task { await model.toggleAudioOnly() } } : nil
        )
    }

    private func problem(
        icon: String,
        title: String,
        message: String,
        actionTitle: String? = nil,
        action: (() -> Void)? = nil
    ) -> some View {
        VStack(spacing: 22) {
            Image(systemName: icon)
                .font(.system(size: 72))
                .foregroundStyle(.tertiary)
            Text(title)
                .font(.title.bold())
            Text(message)
                .font(.title3)
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
                .frame(maxWidth: 900)
            HStack(spacing: 20) {
                if let actionTitle, let action {
                    Button(actionTitle, action: action)
                }
                Button("Close") { dismiss() }
            }
            .padding(.top, 10)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
