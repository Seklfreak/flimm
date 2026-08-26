import Foundation

/// `POST <device_authorization_endpoint>` — RFC 8628.
///
/// The Apple TV never opens a browser: it shows ``userCode`` (or the QR code
/// for ``verificationURIComplete``) and polls the token endpoint until someone
/// approves it on a phone or a laptop.
public struct DeviceAuthorization: Sendable, Hashable, Codable {
    /// The client's half of the pair. Never shown — it goes in the poll body.
    public let deviceCode: String
    /// The short code a human types, e.g. `WDJB-MJHT`.
    public let userCode: String
    /// Where to type it.
    public let verificationURI: URL
    /// The same page with the code already filled in. Optional in the RFC, and
    /// the only thing worth putting in a QR code when it is present.
    public let verificationURIComplete: URL?
    /// When the code stops being accepted.
    public let expiresAt: Date
    /// The provider's minimum seconds between polls; 5 when it says nothing.
    public let interval: Int

    public init(
        deviceCode: String,
        userCode: String,
        verificationURI: URL,
        verificationURIComplete: URL? = nil,
        expiresAt: Date,
        interval: Int = 5
    ) {
        self.deviceCode = deviceCode
        self.userCode = userCode
        self.verificationURI = verificationURI
        self.verificationURIComplete = verificationURIComplete
        self.expiresAt = expiresAt
        self.interval = interval
    }

    /// What to encode in the QR code: the pre-filled URL when the provider
    /// offers one, otherwise the plain verification page.
    public var scannableURI: URL { verificationURIComplete ?? verificationURI }

    /// `WDJBMJHT` → `WDJB-MJHT`. Providers that already group their codes are
    /// left alone; a run of characters is hard to read across a room.
    public var displayCode: String {
        guard !userCode.contains("-"), !userCode.contains(" "), userCode.count == 8 else { return userCode }
        let middle = userCode.index(userCode.startIndex, offsetBy: 4)
        return "\(userCode[..<middle])-\(userCode[middle...])"
    }
}

/// The wire shape, which uses seconds-from-now rather than an instant.
struct DeviceAuthorizationResponse: Decodable {
    let deviceCode: String
    let userCode: String
    let verificationURI: String
    let verificationURIComplete: String?
    let expiresIn: Int?
    let interval: Int?

    enum CodingKeys: String, CodingKey {
        case deviceCode = "device_code"
        case userCode = "user_code"
        case verificationURI = "verification_uri"
        case verificationURIComplete = "verification_uri_complete"
        case expiresIn = "expires_in"
        case interval
    }

    func authorization(now: Date = Date()) throws -> DeviceAuthorization {
        guard let uri = URL(string: verificationURI) else { throw OIDCError.invalidConfiguration }
        return DeviceAuthorization(
            deviceCode: deviceCode,
            userCode: userCode,
            verificationURI: uri,
            verificationURIComplete: verificationURIComplete.flatMap(URL.init(string:)),
            // 15 minutes is the RFC's suggested default for a provider that
            // omits the field.
            expiresAt: now.addingTimeInterval(TimeInterval(expiresIn ?? 900)),
            interval: max(1, interval ?? 5)
        )
    }
}

/// The RFC 8628 polling rules, as pure functions so they can be tested without
/// waiting out a real interval.
enum DeviceFlow {
    /// `slow_down` means "add to your interval and keep going" — it is not a
    /// failure, and a client that treats it as one never signs in.
    static func nextInterval(after code: String, current: Duration, increment: Duration) -> Duration {
        code == "slow_down" ? current + increment : current
    }

    /// The error codes that end the flow. `authorization_pending` and
    /// `slow_down` are not among them: they mean "nobody has approved it yet".
    static func failure(for code: String, description: String?) -> OIDCError? {
        switch code {
        case "authorization_pending", "slow_down": nil
        case "expired_token": .deviceCodeExpired
        case "access_denied": .authorizationFailed("Sign-in was declined.")
        default: .tokenExchangeFailed(description ?? code)
        }
    }
}

extension OIDCClient {
    /// Starts the device grant. Throws ``OIDCError/deviceFlowUnsupported``
    /// when the provider's discovery document has no endpoint for it — on
    /// Apple TV that is the end of the road, since there is no browser.
    public func deviceAuthorize() async throws -> DeviceAuthorization {
        guard let endpoint = configuration.deviceAuthorizationEndpoint else {
            throw OIDCError.deviceFlowUnsupported
        }
        var request = URLRequest(url: endpoint)
        request.httpMethod = "POST"
        request.setValue("application/x-www-form-urlencoded", forHTTPHeaderField: "Content-Type")
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        request.httpBody = Data(OIDCClient.formEncode([
            "client_id": clientID,
            "scope": scopes.joined(separator: " ")
        ]).utf8)

        let data: Data
        let http: HTTPURLResponse
        do {
            let (payload, response) = try await session.data(for: request)
            guard let status = response as? HTTPURLResponse else { throw OIDCError.network("not an HTTP response") }
            data = payload
            http = status
        } catch let error as OIDCError {
            throw error
        } catch {
            throw OIDCError.network(error.localizedDescription)
        }

        // A provider that advertises the endpoint but rejects this client is
        // the same problem from the user's side: the grant is not enabled.
        guard (200..<300).contains(http.statusCode) else {
            if http.statusCode == 400 || http.statusCode == 404 { throw OIDCError.deviceFlowUnsupported }
            throw OIDCError.tokenExchangeFailed("HTTP \(http.statusCode)")
        }
        do {
            return try JSONDecoder().decode(DeviceAuthorizationResponse.self, from: data).authorization()
        } catch let error as OIDCError {
            throw error
        } catch {
            throw OIDCError.tokenExchangeFailed(String(describing: error))
        }
    }

    /// Polls the token endpoint until the code is approved, declined or
    /// expires. Honours `interval`, `slow_down`, `authorization_pending` and
    /// `expired_token`.
    ///
    /// `slowDownIncrement` is a parameter only so the tests do not have to
    /// sit through five real seconds.
    public func pollForDeviceToken(
        _ authorization: DeviceAuthorization,
        slowDownIncrement: Duration = .seconds(5)
    ) async throws -> OIDCTokens {
        var wait = Duration.seconds(authorization.interval)
        while true {
            try await Task.sleep(for: wait)
            guard Date() < authorization.expiresAt else { throw OIDCError.deviceCodeExpired }

            let outcome = try await postToken(form: [
                "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                "device_code": authorization.deviceCode,
                "client_id": clientID
            ])
            switch outcome {
            case .tokens(let tokens):
                return tokens
            case .failure(let code, let description):
                if let failure = DeviceFlow.failure(for: code, description: description) { throw failure }
                wait = DeviceFlow.nextInterval(after: code, current: wait, increment: slowDownIncrement)
            }
        }
    }
}
