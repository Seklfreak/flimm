import CryptoKit
import Foundation

/// A Proof Key for Code Exchange pair (RFC 7636).
///
/// The verifier is a high-entropy random string kept in memory for the length
/// of one sign-in; the challenge is its SHA-256, base64url-encoded without
/// padding, and is what travels in the authorization URL.
public struct PKCE: Sendable, Hashable {
    /// 43–128 characters from the unreserved set.
    public let verifier: String

    public static let method = "S256"

    public init() {
        self.verifier = PKCE.randomVerifier()
    }

    /// For tests and for resuming an interrupted flow.
    public init(verifier: String) {
        self.verifier = verifier
    }

    public var challenge: String {
        let digest = SHA256.hash(data: Data(verifier.utf8))
        return Data(digest).base64URLEncodedString()
    }

    static func randomVerifier(length: Int = 64) -> String {
        // The unreserved set from RFC 7636 §4.1. Generating from this
        // alphabet directly avoids any encoding question later.
        let alphabet = Array("ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        var bytes = [UInt8](repeating: 0, count: length)
        _ = SecRandomCopyBytes(kSecRandomDefault, length, &bytes)
        return String(bytes.map { alphabet[Int($0) % alphabet.count] })
    }

    /// The `state` parameter: opaque, single-use, checked on the callback.
    public static func randomState(length: Int = 32) -> String {
        var bytes = [UInt8](repeating: 0, count: length)
        _ = SecRandomCopyBytes(kSecRandomDefault, length, &bytes)
        return Data(bytes).base64URLEncodedString()
    }
}

extension Data {
    /// base64url without padding — what OAuth uses everywhere.
    func base64URLEncodedString() -> String {
        base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}
