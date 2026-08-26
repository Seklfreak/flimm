import Foundation

/// Runs the browser half of the authorization-code flow.
///
/// A protocol rather than a direct `ASWebAuthenticationSession` call so tests
/// can plug in a canned callback URL. It is the browser half only: tvOS has no
/// browser and uses ``DeviceCodeAuthenticator`` instead — both sit behind
/// ``Authenticating``, which is what ``AuthSession`` actually holds.
public protocol WebAuthenticating: Sendable {
    /// Opens `url` and resolves with the callback URL on `callbackScheme`.
    /// Throws ``OIDCError/authorizationFailed(_:)`` when the user cancels.
    func authenticate(url: URL, callbackScheme: String) async throws -> URL
}

#if os(iOS) || os(visionOS)
import AuthenticationServices
import UIKit

/// `ASWebAuthenticationSession`, which gets the shared cookie jar so an
/// already-signed-in provider session doesn't ask twice.
public final class WebAuthenticationSessionAuthenticator: NSObject, WebAuthenticating, @unchecked Sendable {
    public override init() {
        super.init()
    }

    @MainActor
    public func authenticate(url: URL, callbackScheme: String) async throws -> URL {
        try await withCheckedThrowingContinuation { continuation in
            let session = ASWebAuthenticationSession(url: url, callbackURLScheme: callbackScheme) { callback, error in
                if let callback {
                    continuation.resume(returning: callback)
                } else if let error = error as? ASWebAuthenticationSessionError, error.code == .canceledLogin {
                    continuation.resume(throwing: OIDCError.authorizationFailed("Sign-in was cancelled."))
                } else {
                    continuation.resume(throwing: OIDCError.network(error?.localizedDescription ?? "Sign-in failed."))
                }
            }
            session.presentationContextProvider = self
            session.prefersEphemeralWebBrowserSession = false
            if !session.start() {
                continuation.resume(throwing: OIDCError.network("Couldn't open the sign-in browser."))
            }
        }
    }
}

extension WebAuthenticationSessionAuthenticator: ASWebAuthenticationPresentationContextProviding {
    public func presentationAnchor(for session: ASWebAuthenticationSession) -> ASPresentationAnchor {
        let scene = UIApplication.shared.connectedScenes
            .compactMap { $0 as? UIWindowScene }
            .first { $0.activationState == .foregroundActive }
        return scene?.keyWindow ?? ASPresentationAnchor()
    }
}
#endif
