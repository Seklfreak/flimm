import Foundation

public extension APIClient {
    /// Registers this device for feed notifications. Idempotent — the app
    /// sends it on every launch, because Apple may rotate the token at any
    /// time and the old one then delivers nothing without saying so.
    func registerDevice(token: String, platform: String, environment: PushEnvironment = .current) async throws {
        try await discard(.put, "/me/devices/\(esc(token))", body: PushDeviceInput(platform: platform, environment: environment))
    }

    /// Sign-out: this device stops being the account's.
    func unregisterDevice(token: String) async throws {
        try await discard(.delete, "/me/devices/\(esc(token))")
    }
}
