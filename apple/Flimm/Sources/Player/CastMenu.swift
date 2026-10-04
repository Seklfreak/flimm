import FlimmKit
import SwiftUI

/// "Play on Living Room": hands the video to an Apple TV with Flimm open.
///
/// It is only drawn when there is a screen to hand it to — the receivers ride
/// the remote-control poll that is already open, so this costs nothing to keep
/// up to date and never offers a television that is off. The video moves
/// rather than plays twice: once the television has it, this player closes,
/// and the "playing on…" bar takes over as the way to steer it.
struct CastMenu: View {
    let model: WatchModel
    let hit: CGFloat

    @Environment(RemoteControl.self) private var remote
    @Environment(PlayerCoordinator.self) private var player
    @State private var failed: RemoteReceiver?

    var body: some View {
        if !remote.receivers.isEmpty {
            Menu {
                Section("Play on") {
                    ForEach(remote.receivers) { receiver in
                        Button {
                            Task { await cast(to: receiver) }
                        } label: {
                            Label(receiver.device.isEmpty ? "Apple TV" : receiver.device, systemImage: "appletv")
                        }
                    }
                }
            } label: {
                Image(systemName: "tv")
                    .playerHitTarget(hit)
            }
            .accessibilityLabel("Play on TV")
            .alert(
                "Couldn't reach \(failed?.device ?? "the TV")",
                isPresented: Binding(get: { failed != nil }, set: { if !$0 { failed = nil } })
            ) {
                Button("OK", role: .cancel) {}
            } message: {
                Text("The video is still playing here.")
            }
        }
    }

    /// From where playback is now and in the list it is being played from, so
    /// up next and previous/next carry on on the television as they were here.
    private func cast(to receiver: RemoteReceiver) async {
        do {
            try await remote.cast(
                model.videoId, position: model.engine.currentTime, context: model.context, to: receiver
            )
            player.dismiss()
        } catch {
            failed = receiver
        }
    }
}
