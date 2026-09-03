import FlimmKit
import SwiftUI
import UserNotifications

/// Feed notifications on the phone: the permission, the device token, and
/// what a tapped alert opens.
///
/// The server decides *what* is news (see the notifier in `docs/api.md`);
/// this owns the two things only the device can do — ask the person, and
/// hand Apple's token to the account. The token is re-registered on every
/// launch because Apple may replace it at any time, and the old one then
/// delivers nothing without a word.
@MainActor
@Observable
final class PushCoordinator {
    /// Apple's token for this install, once the system has handed it over.
    private(set) var token: String?
    private(set) var authorization: UNAuthorizationStatus = .notDetermined
    /// What the last tapped notification asked to open. `ContentView` clears
    /// it once the app is far enough along to do so — a tap that launched
    /// the app arrives before there is anything to open it in.
    var pendingLink: PushLink?

    @ObservationIgnored private var client: APIClient?
    /// The registration the server already has, as "token@server", so a
    /// launch that changes neither sends nothing.
    @ObservationIgnored private var registered: String?

    /// Whether a notification would be shown at all.
    var canNotify: Bool {
        switch authorization {
        case .authorized, .provisional, .ephemeral: true
        default: false
        }
    }

    /// Notifications are switched off for the app in Settings; the editor
    /// says so instead of showing a switch that does nothing.
    var isDenied: Bool { authorization == .denied }

    func refreshAuthorization() async {
        authorization = await UNUserNotificationCenter.current().notificationSettings().authorizationStatus
        if canNotify {
            registerWithApple()
        }
    }

    /// Asks the person, the first time a feed is set to notify. Returns
    /// whether they said yes.
    func requestPermission() async -> Bool {
        let granted = (try? await UNUserNotificationCenter.current().requestAuthorization(options: [.alert, .sound, .badge])) ?? false
        await refreshAuthorization()
        return granted
    }

    /// The account this device registers with; nil signs it off.
    func attach(client: APIClient?) {
        self.client = client
        if client != nil, canNotify {
            registerWithApple()
        }
        Task { await syncRegistration() }
    }

    /// Sign-out: the server forgets this device before the session goes.
    func unregister() async {
        guard let token, let client else { return }
        try? await client.unregisterDevice(token: token)
        registered = nil
    }

    /// The app delegate's callback.
    func didRegister(deviceToken: Data) {
        token = DeviceToken.hex(deviceToken)
        Task { await syncRegistration() }
    }

    private func registerWithApple() {
        UIApplication.shared.registerForRemoteNotifications()
    }

    private func syncRegistration() async {
        guard let token, let client else { return }
        let key = token + "@" + client.baseURL.absoluteString
        if registered == key { return }
        let platform = UIDevice.current.userInterfaceIdiom == .pad ? "ipados" : "ios"
        do {
            try await client.registerDevice(token: token, platform: platform)
            registered = key
        } catch {
            // Next launch tries again; there is nothing to show anyone.
        }
    }
}

/// The delegate Apple talks to: the token, and the taps. It owns the
/// coordinator so the callbacks have somewhere to go before SwiftUI is up.
final class AppDelegate: NSObject, UIApplicationDelegate, UNUserNotificationCenterDelegate {
    let push = PushCoordinator()

    func application(
        _ application: UIApplication,
        didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]? = nil
    ) -> Bool {
        UNUserNotificationCenter.current().delegate = self
        Task { await push.refreshAuthorization() }
        return true
    }

    func application(_ application: UIApplication, didRegisterForRemoteNotificationsWithDeviceToken deviceToken: Data) {
        push.didRegister(deviceToken: deviceToken)
    }

    func application(_ application: UIApplication, didFailToRegisterForRemoteNotificationsWithError error: any Error) {
        // A simulator without push support, or no network. The feed's flag
        // is stored server-side either way; the next launch tries again.
    }

    /// An alert that arrives while the app is open is still shown — the
    /// viewer may be in a different feed, and a banner is how they learn
    /// there is something in another.
    nonisolated func userNotificationCenter(
        _ center: UNUserNotificationCenter,
        willPresent notification: UNNotification
    ) async -> UNNotificationPresentationOptions {
        [.banner, .list, .sound]
    }

    nonisolated func userNotificationCenter(_ center: UNUserNotificationCenter, didReceive response: UNNotificationResponse) async {
        // Parsed here, on Apple's queue: the dictionary is not Sendable, the
        // link is.
        let link = PushLink(userInfo: response.notification.request.content.userInfo)
        await MainActor.run { push.pendingLink = link }
    }
}
