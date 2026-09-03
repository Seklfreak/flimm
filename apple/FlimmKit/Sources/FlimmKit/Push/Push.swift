import Foundation

/// Which Apple Push Notification service a device token belongs to.
///
/// A build run from Xcode gets a token for the sandbox, a TestFlight or App
/// Store build one for production, and a notification sent to the wrong host
/// is refused. The app cannot read its own provisioning profile at runtime,
/// but the build configuration is the same fact: only a Debug build carries a
/// development profile here.
public enum PushEnvironment: String, Codable, Sendable, Hashable {
    case production
    case sandbox

    /// The environment this build registers with.
    public static var current: PushEnvironment {
        #if DEBUG
        .sandbox
        #else
        .production
        #endif
    }
}

/// Body for `PUT /me/devices/{token}`.
public struct PushDeviceInput: Codable, Sendable, Hashable {
    public var platform: String
    public var environment: PushEnvironment

    public init(platform: String, environment: PushEnvironment = .current) {
        self.platform = platform
        self.environment = environment
    }
}

/// The device token as the server wants it: the hex of the bytes Apple hands
/// the app delegate.
public enum DeviceToken {
    public static func hex(_ data: Data) -> String {
        data.map { String(format: "%02x", $0) }.joined()
    }
}

/// What a tapped notification opens. The server puts `feed` on every alert
/// and `video` on the ones about a single video; anything else the app is
/// woken with is not one of ours.
public enum PushLink: Hashable, Sendable {
    /// One new video, opened in its feed so *up next* is the rest of it.
    case video(id: String, feedID: String)
    /// A digest — several new videos — opens the feed itself.
    case feed(id: String)

    public init?(userInfo: [AnyHashable: Any]) {
        guard let feed = userInfo["feed"] as? String, !feed.isEmpty else { return nil }
        if let video = userInfo["video"] as? String, !video.isEmpty {
            self = .video(id: video, feedID: feed)
        } else {
            self = .feed(id: feed)
        }
    }
}
