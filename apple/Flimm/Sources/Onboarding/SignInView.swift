import FlimmKit
import SwiftUI

/// Step two: the browser half of Authorization Code + PKCE.
///
/// A failure here is nearly always the provider rejecting the native redirect
/// URI, so the exact value the app sends is on screen rather than buried in a
/// log — it is a one-line change on the provider's client.
struct SignInView: View {
    @Environment(AuthSession.self) private var session
    @State private var isSigningIn = false
    @State private var failure: String?
    @State private var showRedirectHelp = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                header
                if let failure {
                    VStack(alignment: .leading, spacing: 8) {
                        Label("Sign-in didn't complete", systemImage: "person.badge.key")
                            .font(.subheadline.weight(.bold))
                            .foregroundStyle(Palette.danger)
                        Text(failure)
                            .font(.footnote)
                        Button("What redirect URI does this app use?") { showRedirectHelp = true }
                            .font(.footnote.weight(.semibold))
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(14)
                    .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                }
                signInButton
                Button("Use a different server") {
                    Task { await session.forgetServer() }
                }
                .font(.subheadline.weight(.semibold))
            }
            .padding(24)
            .frame(maxWidth: 520)
            .frame(maxWidth: .infinity)
        }
        .background(Palette.background)
        .sheet(isPresented: $showRedirectHelp) { RedirectHelpSheet() }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Image(systemName: "checkmark.seal.fill")
                .font(.system(size: 40))
                .foregroundStyle(Palette.accent)
            Text(session.server?.config.appName ?? "Flimm")
                .font(.largeTitle.bold())
            if let host = session.server?.baseURL.host() {
                Text(host)
                    .font(.subheadline.monospaced())
                    .foregroundStyle(.secondary)
            }
            Text("Sign in with your identity provider to continue.")
                .font(.body)
                .foregroundStyle(.secondary)
                .padding(.top, 4)
        }
        .padding(.top, 32)
    }

    private var signInButton: some View {
        Button {
            Task { await signIn() }
        } label: {
            HStack {
                if isSigningIn { ProgressView().tint(.white) }
                Text(isSigningIn ? "Signing in…" : "Sign in")
                    .font(.headline)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
        }
        .background(Palette.accent, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .foregroundStyle(.white)
        .disabled(isSigningIn)
    }

    private func signIn() async {
        guard !isSigningIn else { return }
        isSigningIn = true
        failure = nil
        defer { isSigningIn = false }
        do {
            try await session.signIn()
        } catch is CancellationError {
            return
        } catch {
            // A cancelled ASWebAuthenticationSession is a tap on "Cancel", not
            // something to shout about.
            let message = AppModel.message(for: error)
            failure = message.localizedCaseInsensitiveContains("cancel") ? nil : message
        }
    }
}

/// The exact native redirect URI, ready to be copied into the provider.
struct RedirectHelpSheet: View {
    @Environment(\.dismiss) private var dismiss
    @State private var copied = false

    var body: some View {
        NavigationStack {
            VStack(alignment: .leading, spacing: 16) {
                Text("""
                Your identity provider must allow this exact native redirect \
                URI on the client Flimm uses, and must grant the \
                `offline_access` scope — without a refresh token this app is \
                signed out as soon as the access token expires.
                """)
                .font(.subheadline)
                .foregroundStyle(.secondary)

                HStack {
                    Text(AppConfig.redirectURI.absoluteString)
                        .font(.callout.monospaced())
                        .textSelection(.enabled)
                    Spacer(minLength: 8)
                    Button {
                        UIPasteboard.general.string = AppConfig.redirectURI.absoluteString
                        copied = true
                    } label: {
                        Image(systemName: copied ? "checkmark" : "doc.on.doc")
                    }
                }
                .padding(14)
                .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))

                Spacer()
            }
            .padding(20)
            .navigationTitle("Redirect URI")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") { dismiss() }
                }
            }
        }
        .presentationDetents([.medium])
    }
}
