import Foundation

/// A `URLProtocol` that answers from a closure instead of the network, and
/// records what it was asked — enough to assert on paths, query order, headers
/// and bodies without a server.
final class StubURLProtocol: URLProtocol {
    struct Recorded: @unchecked Sendable {
        let request: URLRequest
        let body: Data?

        var url: URL? { request.url }
        var path: String? { request.url?.path }
        var query: String? { request.url?.query }
        var method: String? { request.httpMethod }

        func header(_ name: String) -> String? { request.value(forHTTPHeaderField: name) }
    }

    typealias Handler = @Sendable (URLRequest, Data?) throws -> (Int, Data)

    private static let lock = NSLock()
    nonisolated(unsafe) private static var handler: Handler?
    nonisolated(unsafe) private static var log: [Recorded] = []

    /// A session wired to this protocol only.
    static func session(_ handler: @escaping Handler) -> URLSession {
        lock.withLock {
            self.handler = handler
            self.log = []
        }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: configuration)
    }

    /// Always answers 200 with `json`.
    static func session(json: String) -> URLSession {
        session { _, _ in (200, Data(json.utf8)) }
    }

    static var recorded: [Recorded] {
        lock.withLock { log }
    }

    // URLProtocol declares these as class methods, so they cannot be static.
    // swiftlint:disable static_over_final_class
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    // swiftlint:enable static_over_final_class

    override func startLoading() {
        let body = StubURLProtocol.body(of: request)
        let recorded = Recorded(request: request, body: body)
        let handler = StubURLProtocol.lock.withLock { () -> Handler? in
            StubURLProtocol.log.append(recorded)
            return StubURLProtocol.handler
        }
        guard let handler, let url = request.url else {
            client?.urlProtocol(self, didFailWithError: URLError(.unsupportedURL))
            return
        }
        do {
            let (status, data) = try handler(request, body)
            let response = HTTPURLResponse(
                url: url,
                statusCode: status,
                httpVersion: "HTTP/1.1",
                headerFields: ["Content-Type": "application/json"]
            )!
            client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
            client?.urlProtocol(self, didLoad: data)
            client?.urlProtocolDidFinishLoading(self)
        } catch {
            client?.urlProtocol(self, didFailWithError: error)
        }
    }

    override func stopLoading() {}

    /// URLSession moves `httpBody` into `httpBodyStream` before the protocol
    /// sees it, so a body assertion has to drain the stream.
    private static func body(of request: URLRequest) -> Data? {
        if let body = request.httpBody { return body }
        guard let stream = request.httpBodyStream else { return nil }
        stream.open()
        defer { stream.close() }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 4096)
        while stream.hasBytesAvailable {
            let read = stream.read(&buffer, maxLength: buffer.count)
            if read <= 0 { break }
            data.append(buffer, count: read)
        }
        return data
    }
}
