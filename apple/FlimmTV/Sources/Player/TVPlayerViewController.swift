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
        // The captions sit just above the bottom edge and step up when the
        // transport bar appears; the delegate is the only notice AVKit gives.
        controller.delegate = context.coordinator

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
    final class Coordinator: NSObject, AVPlayerViewControllerDelegate {
        let overlay: UIHostingController<TVPlayerOverlay>
        let infoPanel: UIHostingController<TVPlayerInfoPanel>

        private let model: TVWatchModel
        /// The item state is expensive to rebuild, so it is re-applied only
        /// when the model says the item or its sidecars changed.
        private var appliedGeneration = -1
        /// What the transport-bar buttons were last built for. They depend on
        /// the list around the video, which arrives after the item does.
        private var appliedNav: NavAvailability?
        /// Whether the Info tab has been pinned to the panel's width (once).
        private var pinnedInfoPanel = false
        /// The panel's ground; see ``dressInfoPanel(_:)``.
        private let infoPanelGround = UIVisualEffectView(effect: UIBlurEffect(style: .dark))

        nonisolated func playerViewController(
            _ playerViewController: AVPlayerViewController,
            willTransitionToVisibilityOfTransportBar visible: Bool,
            with coordinator: any AVPlayerViewControllerAnimationCoordinator
        ) {
            MainActor.assumeIsolated {
                overlay.rootView = TVPlayerOverlay(model: model, transportBarVisible: visible)
            }
        }

        init(model: TVWatchModel) {
            self.model = model
            self.overlay = UIHostingController(rootView: TVPlayerOverlay(model: model))
            self.infoPanel = UIHostingController(rootView: TVPlayerInfoPanel(model: model))
            super.init()
            infoPanel.title = "Flimm"
            // The panel sits over playing video and AVKit gives a custom tab
            // no ground of its own, so it gets one here — a blur rather than
            // a black fill, which is both what the rest of tvOS does over
            // video and what keeps the picture visible behind the settings
            // you are changing. See `dressInfoPanel()`.
            infoPanel.view.backgroundColor = .clear
        }

        /// Both properties belong to the *item*, not the controller, so they
        /// have to be re-applied every time the model swaps one in — which is
        /// what `itemGeneration` marks.
        func apply(to controller: AVPlayerViewController) {
            pinInfoPanelWidth()
            // Re-checked rather than done once: AVKit builds the panel's
            // hierarchy when the tab is first opened, and can rebuild it.
            dressInfoPanel()
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

        /// AVKit lays a custom Info tab out to its own idea of the content's
        /// size, which leaves the rest of the panel showing the video beside
        /// an opaque tab — a black band that stops short with picture next to
        /// it. Pinning the hosting view to its container's width makes the
        /// ground cover the panel. Width only: the height is AVKit's business,
        /// and constraining it too would be a fight with the panel's own
        /// layout rather than a fix.
        private func pinInfoPanelWidth() {
            guard !pinnedInfoPanel, let host = infoPanel.viewIfLoaded, let parent = host.superview else { return }
            pinnedInfoPanel = true
            host.translatesAutoresizingMaskIntoConstraints = false
            NSLayoutConstraint.activate([
                host.leadingAnchor.constraint(equalTo: parent.leadingAnchor),
                host.trailingAnchor.constraint(equalTo: parent.trailingAnchor)
            ])
        }

        /// The frosted half of the panel's ground: a dark blur, rounded and
        /// inset to sit exactly under the wash the panel itself draws. Blur
        /// rather than a fill because the video is the
        /// thing being configured — quality, subtitles, speed — and a slab
        /// hides what those settings are being judged against. Inset a little
        /// from the panel's edges, because rounded corners read as rounded
        /// only when there is picture beside them.
        ///
        /// It goes into the panel's *parent*, behind the hosting view, rather
        /// than inside it: a `UIHostingController` draws its SwiftUI content
        /// into its own view's layer, so a subview added to that view — even
        /// at index 0 — lands on top of the rows and hides every one of them.
        private func dressInfoPanel() {
            guard let host = infoPanel.viewIfLoaded, let parent = host.superview else { return }
            guard infoPanelGround.superview !== parent else { return }

            let ground = infoPanelGround
            ground.removeFromSuperview()
            ground.translatesAutoresizingMaskIntoConstraints = false
            ground.layer.cornerRadius = TVPlayerInfoPanel.groundRadius
            ground.layer.cornerCurve = .continuous
            ground.clipsToBounds = true

            // The wash that makes the rows readable is the panel's own, so it
            // survives this view not being there; the blur only frosts.
            parent.insertSubview(ground, belowSubview: host)
            NSLayoutConstraint.activate([
                ground.leadingAnchor.constraint(equalTo: host.leadingAnchor, constant: TVPlayerInfoPanel.groundInset),
                ground.trailingAnchor.constraint(equalTo: host.trailingAnchor, constant: -TVPlayerInfoPanel.groundInset),
                ground.topAnchor.constraint(equalTo: host.topAnchor),
                ground.bottomAnchor.constraint(equalTo: host.bottomAnchor)
            ])
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
