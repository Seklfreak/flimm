import Foundation

/// When an advertised activity is worth rewriting.
///
/// A rule of its own, and a pure one, because Handoff cannot be exercised in a
/// simulator at all — it needs two signed-in devices in the same room. What
/// *can* be checked here is the only thing this feature decides for itself:
/// how often it speaks. Everything else is a payload and a plist.
public enum HandoffRule {
    /// How far playback must move before the activity is rewritten. The same
    /// ten seconds the progress heartbeat uses: a receiving device that lands
    /// nine seconds late lands inside the same sentence.
    public static let positionStep: Double = 10

    /// Whether `next` says something a device that heard `published` does not
    /// already know.
    public static func shouldPublish(_ published: Continuation?, _ next: Continuation) -> Bool {
        guard let published else { return true }
        guard published.server == next.server, published.title == next.title else { return true }
        switch (published.destination, next.destination) {
        case (.watch(let oldId, let oldPosition, let oldContext), .watch(let newId, let newPosition, let newContext)):
            guard oldId == newId, oldContext == newContext else { return true }
            // Includes a seek backwards, which moves as much as one forwards.
            return abs(newPosition - oldPosition) >= positionStep
        case (.page(let old), .page(let new)):
            return old != new
        default:
            return true
        }
    }
}

/// Advertises what this device is doing, so a device beside it can take over.
///
/// A publisher asks the app what it is doing rather than being told, for the
/// reason ``RemotePublisher`` does: the states worth handing off are the ones
/// nobody thinks to announce — a paused player, a screen somebody is reading —
/// and a publisher driven by pushes goes quiet exactly then. Owning the tick
/// means a still app keeps saying where it is.
///
/// One activity, replaced in place rather than a new one per video: an
/// `NSUserActivity` that becomes current is what the devices around it are
/// showing, and swapping the object makes that offer blink out of somebody's
/// hand between two videos.
@MainActor
public final class HandoffPublisher {
    /// Reads what the app is doing right now. `nil` means "nothing worth
    /// continuing", which withdraws the offer.
    public typealias StateProvider = @MainActor () -> Continuation?

    /// How often the rule is asked. Cheap: all but one tick in ten answers no,
    /// and a tick that does publish writes a few hundred bytes.
    private static let tick = Duration.seconds(2)

    private var activity: NSUserActivity?
    private var published: Continuation?
    private var provider: StateProvider?
    private var ticker: Task<Void, Never>?

    public init() {}

    /// Begins advertising. Safe to call again; the previous run is stopped
    /// first.
    public func start(state: @escaping StateProvider) {
        ticker?.cancel()
        provider = state
        published = nil
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                self?.refresh()
                try? await Task.sleep(for: Self.tick)
            }
        }
    }

    /// Withdraws the offer and stops. Called on sign-out as well as teardown:
    /// an activity outlives the app that published it, and a signed-out phone
    /// must not still be offering a stranger's library to the room.
    public func stop() {
        ticker?.cancel()
        ticker = nil
        provider = nil
        published = nil
        activity?.resignCurrent()
        activity?.invalidate()
        activity = nil
    }

    private func refresh() {
        guard let next = provider?() else {
            // Nothing to continue: the player closed, or the session ended.
            guard activity != nil else { return }
            published = nil
            activity?.resignCurrent()
            activity?.invalidate()
            activity = nil
            return
        }
        guard HandoffRule.shouldPublish(published, next) else { return }
        publish(next)
    }

    private func publish(_ continuation: Continuation) {
        let activity = self.activity ?? NSUserActivity(activityType: Continuation.activityType)
        activity.title = continuation.title
        activity.userInfo = continuation.userInfo
        // The other half of Handoff: a Mac with no Flimm app opens the web
        // client at the same place rather than offering nothing.
        activity.webpageURL = continuation.webpageURL
        activity.isEligibleForHandoff = true
        #if os(iOS)
        // A private library is not something to publish to Spotlight or to
        // Siri's suggestions. Handoff is a device in the same room; those are
        // indexes that outlive the moment.
        activity.isEligibleForSearch = false
        activity.isEligibleForPrediction = false
        #endif
        self.activity = activity
        activity.becomeCurrent()
        published = continuation
    }
}
