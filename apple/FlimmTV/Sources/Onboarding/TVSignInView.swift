import FlimmKit
import Observation
import SwiftUI

/// Step two on Apple TV: the OIDC **device authorization grant** (RFC 8628).
///
/// `ASWebAuthenticationSession` does not exist here and there is no browser to
/// fall back to, so the TV shows a short code and a QR code and polls the token
/// endpoint while someone approves it on a phone. A provider that does not
/// offer the grant is a hard stop, and the screen says exactly what has to
/// change rather than failing vaguely.
@MainActor
@Observable
final class TVSignInModel {
    enum Phase: Equatable {
        case idle
        /// Discovery and the device-authorization request.
        case starting
        /// Waiting for a human. The code is on screen.
        case waiting(DeviceAuthorization)
        case failed(String)
        /// The provider has no `device_authorization_endpoint`.
        case unsupported
    }

    private(set) var phase: Phase = .idle

    func start(session: AuthSession) async {
        // Re-entrant guard only: `.task` fires once and "Try again" fires
        // again, but a poll already in flight must not be started twice.
        guard !isRunning else { return }
        phase = .starting
        // The authenticator publishes the code the moment the provider issues
        // it, so the screen fills in while polling is still running.
        let authenticator = DeviceCodeAuthenticator { [weak self] authorization in
            await self?.present(authorization)
        }
        do {
            try await session.signIn(using: authenticator)
        } catch is CancellationError {
            phase = .idle
        } catch OIDCError.deviceFlowUnsupported {
            phase = .unsupported
        } catch {
            phase = .failed(AppModel.message(for: error))
        }
    }

    func reset() {
        phase = .idle
    }

    private var isRunning: Bool {
        switch phase {
        case .starting, .waiting: true
        case .idle, .failed, .unsupported: false
        }
    }

    private func present(_ authorization: DeviceAuthorization) {
        phase = .waiting(authorization)
    }
}

struct TVSignInView: View {
    @Environment(AuthSession.self) private var session
    @State private var model = TVSignInModel()

    var body: some View {
        HStack(alignment: .center, spacing: 90) {
            explanation
            Divider()
            action
        }
        .padding(TVMetrics.margin)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .task { await model.start(session: session) }
    }

    // MARK: - Left column

    private var explanation: some View {
        VStack(alignment: .leading, spacing: 18) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 90))
                .foregroundStyle(Palette.accent)
            Text(session.server?.config.appName ?? "Flimm")
                .font(.system(size: 62, weight: .bold))
            if let host = session.server?.baseURL.host() {
                Text(host)
                    .font(.title3.monospaced())
                    .foregroundStyle(.secondary)
            }
            Text(subtitle)
                .font(.title3)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            Button("Use a different server") {
                Task { await session.forgetServer() }
            }
            .padding(.top, 12)
        }
        .frame(maxWidth: 640, alignment: .leading)
    }

    private var subtitle: String {
        switch model.phase {
        case .unsupported:
            return "This server's identity provider can't sign in a TV."
        case .failed:
            return "Sign-in didn't complete."
        default:
            return "Sign in from your phone or computer — there's nothing to type here."
        }
    }

    // MARK: - Right column

    @ViewBuilder
    private var action: some View {
        switch model.phase {
        case .idle, .starting:
            TVLoadingState(label: "Asking your provider for a code…")
        case .waiting(let authorization):
            code(authorization)
        case .unsupported:
            unsupported
        case .failed(let message):
            VStack(spacing: 24) {
                TVErrorState(message: message)
                Button("Try again") {
                    Task { await model.start(session: session) }
                }
            }
        }
    }

    private func code(_ authorization: DeviceAuthorization) -> some View {
        HStack(alignment: .center, spacing: 50) {
            QRCodeView(text: authorization.scannableURI.absoluteString)
            VStack(alignment: .leading, spacing: 20) {
                Text("Scan the code, or go to")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text(authorization.verificationURI.absoluteString)
                    .font(.title2.monospaced().weight(.semibold))
                    .lineLimit(2)
                    .minimumScaleFactor(0.6)
                // A code read across a room has to be large, high contrast and
                // grouped — this is the whole interaction.
                Text(authorization.displayCode)
                    .font(.system(size: 76, weight: .heavy, design: .monospaced))
                    .kerning(6)
                    .padding(.vertical, 18)
                    .padding(.horizontal, 28)
                    .background(Palette.raised, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
                HStack(spacing: 12) {
                    ProgressView()
                    Text("Waiting for you to approve it…")
                        .font(.body)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }

    private var unsupported: some View {
        VStack(alignment: .leading, spacing: 18) {
            Label("Device sign-in isn't available", systemImage: "person.badge.key")
                .font(.title2.bold())
                .foregroundStyle(Palette.danger)
            Text("""
            Apple TV has no browser, so Flimm signs in with the OIDC device \
            authorization grant (RFC 8628). This server's provider doesn't \
            advertise a device_authorization_endpoint.
            """)
            .font(.title3)
            .foregroundStyle(.secondary)
            .fixedSize(horizontal: false, vertical: true)
            Text("""
            Enable the device authorization grant for the same OIDC client id \
            the web and phone apps use, keep the offline_access scope, then \
            try again. Signing in on a phone, iPad or the web needs no change.
            """)
            .font(.body)
            .foregroundStyle(.tertiary)
            .fixedSize(horizontal: false, vertical: true)

            Button("Try again") {
                Task { await model.start(session: session) }
            }
            .padding(.top, 12)
        }
        .frame(maxWidth: 760, alignment: .leading)
    }
}
