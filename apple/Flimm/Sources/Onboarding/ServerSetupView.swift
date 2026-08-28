import FlimmKit
import SwiftUI

/// A probe failure, phrased for someone setting the app up.
///
/// The four cases are genuinely different problems with different fixes, so
/// they get different words rather than one "couldn't connect".
struct SetupProblem: Identifiable {
    let id = UUID()
    let icon: String
    let title: String
    let detail: String
    /// What to actually do about it.
    let hint: String?

    init(_ error: any Error) {
        switch error as? ServerProbeError {
        case .invalidURL:
            icon = "link.badge.plus"
            title = "That isn't a web address"
            detail = "Enter the address you use to open Flimm in a browser."
            hint = "For example flimm.example.com — https:// is added for you."
        case .unreachable(let detail):
            icon = "wifi.exclamationmark"
            title = "Couldn't reach that server"
            self.detail = detail
            hint = """
            A Flimm server is often only reachable on the home network or over \
            a VPN. Check the address, and that this device is on that network.
            """
        case .notAFlimmServer:
            icon = "questionmark.square.dashed"
            title = "That isn't a Flimm server"
            detail = "Something answered at that address, but it didn't look like Flimm."
            hint = "Use the address of the Flimm app itself, not TubeArchivist."
        case .oidcNotConfigured:
            icon = "person.badge.key"
            title = "That server can't sign anyone in"
            detail = "It has no OIDC provider configured, so this app has no way to sign in."
            hint = "Set OIDC_ISSUER and OIDC_CLIENT_ID on the server, then try again."
        case nil:
            icon = "exclamationmark.triangle"
            title = "Couldn't connect"
            detail = AppModel.message(for: error)
            hint = nil
        }
    }
}

/// Step one of setup: the server URL is the only thing anyone types.
/// `GET /api/v1/config` validates it *and* configures the sign-in flow.
struct ServerSetupView: View {
    @Environment(AuthSession.self) private var session
    @State private var address = ""
    @State private var isProbing = false
    @State private var problem: SetupProblem?
    @FocusState private var addressFocused: Bool

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                header
                field
                if let problem {
                    ProblemCard(problem: problem)
                }
                connectButton
                Text("Flimm is self-hosted: this app talks only to your own server.")
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
            .padding(24)
            .frame(maxWidth: 520)
            .frame(maxWidth: .infinity)
        }
        .background(Palette.background)
        .onAppear {
            addressFocused = true
            Analytics.screen(.server)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Image(systemName: "play.rectangle.on.rectangle.fill")
                .font(.system(size: 40))
                .foregroundStyle(Palette.accent)
            Text("Connect to Flimm")
                .font(.largeTitle.bold())
            Text("Enter the address of your Flimm server to get started.")
                .font(.body)
                .foregroundStyle(.secondary)
        }
        .padding(.top, 32)
    }

    private var field: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("SERVER ADDRESS")
                .font(.caption2.weight(.bold))
                .foregroundStyle(.secondary)
            TextField("flimm.example.com", text: $address)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)
                .textContentType(.URL)
                .submitLabel(.go)
                .focused($addressFocused)
                .onSubmit { Task { await connect() } }
                .padding(14)
                .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        }
    }

    private var connectButton: some View {
        Button {
            Task { await connect() }
        } label: {
            HStack {
                if isProbing { ProgressView().tint(.white) }
                Text(isProbing ? "Checking…" : "Continue")
                    .font(.headline)
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
        }
        .background(Palette.accent, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .foregroundStyle(.white)
        .disabled(isProbing || address.trimmingCharacters(in: .whitespaces).isEmpty)
        .opacity(address.trimmingCharacters(in: .whitespaces).isEmpty ? 0.5 : 1)
    }

    private func connect() async {
        guard !isProbing else { return }
        isProbing = true
        problem = nil
        defer { isProbing = false }
        do {
            try await session.connect(to: address)
        } catch {
            problem = SetupProblem(error)
        }
    }
}

struct ProblemCard: View {
    let problem: SetupProblem

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label(problem.title, systemImage: problem.icon)
                .font(.subheadline.weight(.bold))
                .foregroundStyle(Palette.danger)
            Text(problem.detail)
                .font(.footnote)
            if let hint = problem.hint {
                Text(hint)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
    }
}
