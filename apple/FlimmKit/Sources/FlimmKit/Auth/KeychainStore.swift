import Foundation
import Security

/// Where refresh tokens live. A protocol so tests never touch the real
/// Keychain — and so a tvOS device-code flow can plug in its own store later.
public protocol SecretStore: Sendable {
    func read(_ key: String) throws -> Data?
    func write(_ key: String, _ value: Data) throws
    func delete(_ key: String) throws
}

public enum KeychainError: Error, Sendable, Equatable {
    case status(OSStatus)
}

/// Keychain-backed ``SecretStore``.
///
/// > Note: an *unsigned* simulator build cannot use the Keychain at all, and
/// > every read comes back empty — which looks exactly like "signed out right
/// > after signing in". Sign the simulator build.
public struct KeychainStore: SecretStore {
    private let service: String
    private let accessGroup: String?

    public init(service: String = "dev.winktech.flimm", accessGroup: String? = nil) {
        self.service = service
        self.accessGroup = accessGroup
    }

    private func baseQuery(_ key: String) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: key
        ]
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        return query
    }

    public func read(_ key: String) throws -> Data? {
        var query = baseQuery(key)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne

        var item: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &item)
        switch status {
        case errSecSuccess: return item as? Data
        case errSecItemNotFound: return nil
        default: throw KeychainError.status(status)
        }
    }

    public func write(_ key: String, _ value: Data) throws {
        let query = baseQuery(key)
        let attributes: [String: Any] = [
            kSecValueData as String: value,
            // Tokens are only useful while the device is unlocked, and must
            // not travel to a restored backup on another device.
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly
        ]

        let update = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if update == errSecSuccess { return }
        guard update == errSecItemNotFound else { throw KeychainError.status(update) }

        var insert = query
        insert.merge(attributes) { _, new in new }
        let status = SecItemAdd(insert as CFDictionary, nil)
        guard status == errSecSuccess else { throw KeychainError.status(status) }
    }

    public func delete(_ key: String) throws {
        let status = SecItemDelete(baseQuery(key) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else {
            throw KeychainError.status(status)
        }
    }
}

/// Non-persistent ``SecretStore`` for tests and previews.
public final class InMemorySecretStore: SecretStore, @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String: Data] = [:]

    public init() {}

    public func read(_ key: String) throws -> Data? {
        lock.withLock { items[key] }
    }

    public func write(_ key: String, _ value: Data) throws {
        lock.withLock { items[key] = value }
    }

    public func delete(_ key: String) throws {
        lock.withLock { items[key] = nil }
    }
}
