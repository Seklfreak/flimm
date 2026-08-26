import Foundation

/// Errors from `/api/v1`, mapped from the contract's *Errors* section.
///
/// Note that 404 carries no information: the backend answers unknown *and*
/// unauthorized resources with 404 so existence is never leaked. Treat it as
/// "not yours or not there", never as "deleted".
public enum APIError: Error, Sendable, Equatable {
    /// The base URL and path could not be combined into a URL.
    case invalidURL
    /// Still 401 after a refresh attempt — the session is over.
    case unauthorized
    /// 404: unknown resource, or one belonging to another user.
    case notFound
    /// 400.
    case badRequest(String)
    /// 403.
    case forbidden(String)
    /// 502 — `{"error": "tubearchivist unavailable"}`.
    case upstreamUnavailable(String)
    /// Any other non-2xx status.
    case http(status: Int, message: String)
    /// The response was not the JSON the contract describes.
    case decoding(String)
    /// URLSession failed: offline, TLS, DNS, timeout. Never a reason to sign
    /// a user out — the server may simply be off the public internet.
    case transport(String)

    /// Transport failures are transient; everything else is an answer.
    public var isTransient: Bool {
        switch self {
        case .transport, .upstreamUnavailable: true
        case .http(let status, _): status >= 500
        default: false
        }
    }

    public var errorMessage: String {
        switch self {
        case .invalidURL: "The server URL is not valid."
        case .unauthorized: "Signed out."
        case .notFound: "Not found."
        case .badRequest(let m), .forbidden(let m), .upstreamUnavailable(let m): m
        case .http(let status, let m): m.isEmpty ? "HTTP \(status)" : m
        case .decoding(let m): "Unexpected response: \(m)"
        case .transport(let m): m
        }
    }

    static func fromStatus(_ status: Int, message: String) -> APIError {
        switch status {
        case 400: .badRequest(message.isEmpty ? "Bad request." : message)
        case 401: .unauthorized
        case 403: .forbidden(message.isEmpty ? "Forbidden." : message)
        case 404: .notFound
        case 502: .upstreamUnavailable(message.isEmpty ? "tubearchivist unavailable" : message)
        default: .http(status: status, message: message)
        }
    }
}

extension APIError: LocalizedError {
    public var errorDescription: String? { errorMessage }
}
