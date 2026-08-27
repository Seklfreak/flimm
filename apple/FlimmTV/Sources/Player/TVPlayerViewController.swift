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
/// interstitials, and previous/next as transport-bar buttons.
struct TVPlayerViewController: UIViewControllerRepresentable {
    let model: TVWatchModel

    func makeCoordinator() -> Coordinator {
        Coordinator(model: model)
    }

    func makeUIViewController(context: Context) -> AVPlayerViewController {
        let controller = AVPlayerViewController()
        controller.player = model.player
        // AVKit's own skipping: click left/right to move ±10s inside this
        // video, swipe to scrub. Stepping through the list is what the
        // transport-bar buttons below are for — mapping it onto the skip
        // gestures (`skippingBehavior = .skipItem`) took the scrubber away
        // from the viewer, which is the one thing the transport bar is for.
        controller.skippingBehavior = .default
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
        controller.player = nil
    }

    @MainActor
    final class Coordinator: NSObject {
        let overlay: UIHostingController<TVPlayerOverlay>
        let infoPanel: UIHostingController<TVPlayerInfoPanel>

        private let model: TVWatchModel
        /// The item state is expensive to rebuild, so it is re-applied only
        /// when the model says the item or its sidecars changed.
        private var appliedGeneration = -1
        /// What the transport-bar buttons were last built for. They depend on
        /// the list around the video, which arrives after the item does.
        private var appliedNav: NavAvailability?

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
            let nav = NavAvailability(previous: model.canGoPrevious, next: model.canGoNext)
            if nav != appliedNav {
                appliedNav = nav
                controller.transportBarCustomMenuItems = transportBarItems(nav)
            }
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

        /// Previous/next, as buttons in the transport bar where every other
        /// player on the platform puts them. A direction the list cannot go is
        /// left out rather than shown dead.
        private func transportBarItems(_ nav: NavAvailability) -> [UIMenuElement] {
            var items: [UIMenuElement] = []
            if nav.previous {
                items.append(UIAction(
                    title: "Previous video",
                    image: UIImage(systemName: "backward.end.fill")
                ) { [model] _ in
                    Task { @MainActor in await model.goPrevious() }
                })
            }
            if nav.next {
                items.append(UIAction(
                    title: "Next video",
                    image: UIImage(systemName: "forward.end.fill")
                ) { [model] _ in
                    Task { @MainActor in await model.goNext() }
                })
            }
            return items
        }
    }
}

/// Which way the list around the video can be stepped. The transport-bar
/// buttons are rebuilt when it changes, which is later than the item arrives:
/// `nav` and `up-next` are sidecars, fetched after playback starts.
private struct NavAvailability: Equatable {
    let previous: Bool
    let next: Bool
}
