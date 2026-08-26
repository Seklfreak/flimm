import AVKit
import FlimmKit
import SwiftUI

/// `AVPlayerViewController`, wrapped.
///
/// Everything a TV viewer expects from a player — the transport bar, the scrub
/// preview, the Info panel, the chapter list, the audio and subtitle tabs, the
/// Siri Remote gestures — is that controller's, and reimplementing any of it in
/// SwiftUI would be worse in every way. What this bridge adds is the four
/// things it cannot know about: the bearer-authenticated asset (set up in
/// ``TVWatchModel``), Flimm's chapters as navigation markers, SponsorBlock as
/// interstitials, and previous/next mapped onto the skip gestures.
struct TVPlayerViewController: UIViewControllerRepresentable {
    let model: TVWatchModel

    func makeCoordinator() -> Coordinator {
        Coordinator(model: model)
    }

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = model.player
        controller.delegate = context.coordinator
        // Turns the remote's skip gestures into "previous/next video" instead
        // of ±10s, which is what a feed or playlist run wants.
        controller.skippingBehavior = .skipItem
        controller.customInfoViewControllers = [context.coordinator.infoPanel]

        let overlay = context.coordinator.overlay
        if let host = controller.contentOverlayView {
            overlay.view.backgroundColor = .clear
            overlay.view.translatesAutoresizingMaskIntoConstraints = false
            host.addSubview(overlay.view)
            NSLayoutConstraint.activate([
                overlay.view.leadingAnchor.constraint(equalTo: host.leadingAnchor),
                overlay.view.trailingAnchor.constraint(equalTo: host.trailingAnchor),
                overlay.view.topAnchor.constraint(equalTo: host.topAnchor),
                overlay.view.bottomAnchor.constraint(equalTo: host.bottomAnchor)
            ])
        }
        return controller
    }

    func updateUIViewController(_ controller: AVPlayerViewController, context: Context) {
        context.coordinator.apply(to: controller)
    }

    static func dismantleUIViewController(_ controller: AVPlayerViewController, coordinator: Coordinator) {
        controller.delegate = nil
        controller.player = nil
    }

    @MainActor
    final class Coordinator: NSObject, @preconcurrency AVPlayerViewControllerDelegate {
        let overlay: UIHostingController<TVPlayerOverlay>
        let infoPanel: UIHostingController<TVPlayerInfoPanel>

        private let model: TVWatchModel
        /// The item state is expensive to rebuild, so it is re-applied only
        /// when the model says the item or its sidecars changed.
        private var appliedGeneration = -1

        init(model: TVWatchModel) {
            self.model = model
            self.overlay = UIHostingController(rootView: TVPlayerOverlay(model: model))
            self.infoPanel = UIHostingController(rootView: TVPlayerInfoPanel(model: model))
            super.init()
            infoPanel.title = "Flimm"
        }

        /// Both properties belong to the *item*, not the controller, so they
        /// have to be re-applied every time the model swaps one in — which is
        /// what `itemGeneration` marks.
        func apply(to controller: AVPlayerViewController) {
            guard appliedGeneration != model.itemGeneration else { return }
            guard let item = controller.player?.currentItem else { return }
            appliedGeneration = model.itemGeneration
            item.interstitialTimeRanges = model.interstitials
            let duration = model.video?.duration ?? 0
            if let markers = TVPlayerMarkers.navigationMarkers(for: model.chapters, duration: duration) {
                item.navigationMarkerGroups = [markers]
            } else {
                item.navigationMarkerGroups = []
            }
        }

        // MARK: - AVPlayerViewControllerDelegate

        func skipToNextItem(for playerViewController: AVPlayerViewController) {
            Task { await model.goNext() }
        }

        func skipToPreviousItem(for playerViewController: AVPlayerViewController) {
            Task { await model.goPrevious() }
        }
    }
}
