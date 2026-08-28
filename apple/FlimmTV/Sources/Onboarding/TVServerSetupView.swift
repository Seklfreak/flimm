import FlimmKit
import SwiftUI

/// A probe failure, phrased for someone setting the app up. The four cases are
/// genuinely different problems with different fixes, so they get different
/// words rather than one "couldn't connect".
struct TVSetupProblem: Identifiable {
    let id = UUID()
    let icon: String
    let title: String
    let detail: String
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
            a VPN. Check the address, and that this Apple TV is on that network.
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

/// Step one of setup: the server URL is the only thing anyone types — which on
/// a TV is worth minimising, so it is one field and nothing else.
/// `GET /api/v1/config` validates it *and* configures the sign-in flow.
struct TVServerSetupView: View {
    @Environment(AuthSession.self) private var session

    @State private var address = ""
    @State private var isProbing = false
    @State private var problem: TVSetupProblem?

    var body: some View {
        HStack(alignment: .top, spacing: 80) {
            VStack(alignment: .leading, spacing: 20) {
                Image(systemName: "play.rectangle.on.rectangle.fill")
                    .font(.system(size: 90))
                    .foregroundStyle(Palette.accent)
                Text("Connect to Flimm")
                    .font(.system(size: 62, weight: .bold))
                Text("Enter the address of your Flimm server to get started.")
                    .font(.title3)
                    .foregroundStyle(.secondary)
                Text("Flimm is self-hosted: this app talks only to your own server.")
                    .font(.body)
                    .foregroundStyle(.tertiary)
            }
            .frame(maxWidth: 700, alignment: .leading)

            VStack(alignment: .leading, spacing: 24) {
                TextField("flimm.example.com", text: $address)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .keyboardType(.URL)
                    .onSubmit { Task { await connect() } }

                Button {
                    Task { await connect() }
                } label: {
                    Text(isProbing ? "Checking…" : "Continue")
                        .frame(maxWidth: .infinity)
                }
                .disabled(isProbing || address.trimmingCharacters(in: .whitespaces).isEmpty)

                if let problem {
                    TVProblemCard(problem: problem)
                }
            }
            .frame(maxWidth: 700, alignment: .leading)
        }
        .padding(TVMetrics.margin)
        .onAppear { Analytics.screen(.server) }
    }
}

struct TVProblemCard: View {
    let problem: TVSetupProblem

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Label(problem.title, systemImage: problem.icon)
                .font(.headline)
                .foregroundStyle(Palette.danger)
            Text(problem.detail)
                .font(.body)
            if let hint = problem.hint {
                Text(hint)
                    .font(.body)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(24)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
    }
}

private extension TVServerSetupView {
    func connect() async {
        guard !isProbing else { return }
        isProbing = true
        problem = nil
        defer { isProbing = false }
        do {
            try await session.connect(to: address)
        } catch {
            problem = TVSetupProblem(error)
        }
    }
}
