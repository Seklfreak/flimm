import AVFoundation
import Darwin
import XCTest

/// Empirical proof of how far `AVURLAssetHTTPHeaderFieldsKey` actually reaches.
///
/// The whole authenticated-playback design rests on one assumption: that the
/// custom `Authorization` header handed to `AVURLAsset` rides along on *every*
/// request AVFoundation makes for an HLS stream — the playlist, the fMP4
/// initialization segment, each media segment, and the periodic re-reads of a
/// growing EVENT playlist — not just the first playlist fetch. Documentation is
/// silent on it, so these tests answer it with a real ffmpeg-built stream, a
/// real socket server, and a real `AVPlayer`.
///
/// The fixture is generated at test time (never committed) and the suite skips
/// cleanly when ffmpeg is not installed.
final class HLSHeaderForwardingTests: XCTestCase {
    static let token = "Bearer test-token"

    private var server: RecordingHTTPServer?
    private var fixture: URL?

    override func setUpWithError() throws {
        fixture = try HLSFixture.make()
    }

    override func tearDownWithError() throws {
        server?.stop()
        server = nil
        if let fixture { try? FileManager.default.removeItem(at: fixture) }
        fixture = nil
    }

    // MARK: - The load-bearing question

    /// Plays the VOD stream headlessly and asserts the header on the playlist,
    /// on `init.mp4`, and on the `.m4s` media segments.
    func testHeaderIsForwardedToPlaylistInitSegmentAndMediaSegments() throws {
        let server = try startServer()
        let item = playerItem(for: "index.m3u8", port: server.port)
        let player = AVPlayer(playerItem: item)
        player.play()

        let advanced = pump(timeout: 20) {
            item.status == .readyToPlay && player.currentTime().seconds > 2.5
        }
        player.pause()
        let hits = server.recorded
        report("vod", hits)

        XCTAssertEqual(item.status, .readyToPlay, "item error: \(String(describing: item.error))")
        XCTAssertTrue(advanced, "headless playback never passed 2.5s — segments may not have been fetched")

        assertFetchedWithToken(hits, matching: { $0.hasSuffix(".m3u8") }, what: "playlist")
        assertFetchedWithToken(hits, matching: { $0.hasSuffix("init.mp4") }, what: "init segment")
        assertFetchedWithToken(hits, matching: { $0.hasSuffix(".m4s") }, what: "media segment")

        // More than one media segment proves it is not merely the first request
        // of the session that is decorated.
        let segments = Set(hits.filter { $0.path.hasSuffix(".m4s") }.map(\.path))
        XCTAssertGreaterThan(segments.count, 1, "expected several distinct segments, saw \(segments)")
    }

    /// A live/EVENT playlist is re-read on a timer. Each re-read is a fresh
    /// request, so it is a separate question whether it still carries the header.
    func testHeaderIsForwardedToEventPlaylistReReads() throws {
        let reloads = LockedCounter()
        let server = try startServer { hit in
            guard hit.path == "/event.m3u8" else { return nil }
            let visible = min(3, reloads.next())
            return HTTPResponse(contentType: "application/vnd.apple.mpegurl", body: Data(Self.eventPlaylist(visible).utf8))
        }
        let item = playerItem(for: "event.m3u8", port: server.port)
        let player = AVPlayer(playerItem: item)
        player.play()

        // Two playlist fetches are the minimum that proves a *re*-read; give the
        // player a window comfortably longer than the 2s target duration.
        _ = pump(timeout: 20) { server.recorded.filter { $0.path == "/event.m3u8" }.count >= 3 }
        player.pause()
        let hits = server.recorded
        report("event", hits)

        let playlistHits = hits.filter { $0.path == "/event.m3u8" }
        XCTAssertGreaterThanOrEqual(playlistHits.count, 2, "playlist was never re-read, so this proves nothing")
        XCTAssertTrue(playlistHits.allSatisfy(\.hasToken), "a playlist re-read dropped the header")
    }

    /// Negative control. Without it the assertions above prove nothing: a
    /// recorder that reported "token present" unconditionally would pass them
    /// whatever AVFoundation actually sent.
    func testRecorderSeesAMissingAuthorizationHeader() throws {
        let server = try startServer()
        let url = URL(string: "http://127.0.0.1:\(server.port)/index.m3u8")!
        let done = expectation(description: "unauthenticated GET")
        URLSession(configuration: .ephemeral).dataTask(with: url) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
        report("control", server.recorded)
        XCTAssertEqual(server.recorded.map(\.hasToken), [false])
    }

    // MARK: - What a 503 on the playlist looks like from the outside

    /// Records the failure shape so callers know which observation to hang error
    /// handling off: `status == .failed` plus `item.error`, or the
    /// `failedToPlayToEndTime` notification.
    func testPlaylistServiceUnavailableSurfacesAsItemFailure() throws {
        let notified = LockedCounter()
        let server = try startServer { hit in
            guard hit.path.hasSuffix(".m3u8") else { return nil }
            return HTTPResponse(status: 503, contentType: "text/plain", body: Data("busy".utf8),
                                extraHeaders: ["Retry-After": "1"])
        }
        let item = playerItem(for: "index.m3u8", port: server.port)
        let observer = NotificationCenter.default.addObserver(
            forName: AVPlayerItem.failedToPlayToEndTimeNotification, object: item, queue: nil
        ) { _ in notified.next() }
        defer { NotificationCenter.default.removeObserver(observer) }

        let player = AVPlayer(playerItem: item)
        player.play()
        _ = pump(timeout: 20) { item.status == .failed }
        player.pause()

        let error = item.error as NSError?
        print("[hls-503] status=\(item.status.rawValue) domain=\(error?.domain ?? "-") code=\(error?.code ?? 0)")
        print("[hls-503] description=\(error?.localizedDescription ?? "-")")
        print("[hls-503] underlying=\(String(describing: error?.userInfo[NSUnderlyingErrorKey]))")
        print("[hls-503] failedToPlayToEndTime fired=\(notified.value)")
        XCTAssertEqual(item.status, .failed, "a 503 playlist should fail the item")
    }

    // MARK: - Helpers

    private func startServer(_ handler: RecordingHTTPServer.Handler? = nil) throws -> RecordingHTTPServer {
        let root = try XCTUnwrap(fixture)
        let server = try RecordingHTTPServer(root: root, handler: handler)
        self.server = server
        return server
    }

    /// The asset under test: the header option is the entire point of the file.
    private func playerItem(for name: String, port: UInt16) -> AVPlayerItem {
        let url = URL(string: "http://127.0.0.1:\(port)/\(name)")!
        let asset = AVURLAsset(url: url, options: [
            "AVURLAssetHTTPHeaderFieldsKey": ["Authorization": Self.token]
        ])
        return AVPlayerItem(asset: asset)
    }

    /// Spins the main run loop — AVFoundation delivers on it — until `condition`
    /// holds or the budget runs out.
    private func pump(timeout: TimeInterval, until condition: () -> Bool) -> Bool {
        let deadline = Date().addingTimeInterval(timeout)
        while Date() < deadline {
            if condition() { return true }
            RunLoop.current.run(until: Date().addingTimeInterval(0.05))
        }
        return condition()
    }

    private func assertFetchedWithToken(_ hits: [HTTPHit], matching: (String) -> Bool, what: String) {
        let matched = hits.filter { matching($0.path) }
        XCTAssertFalse(matched.isEmpty, "no \(what) request was ever made")
        XCTAssertTrue(matched.allSatisfy(\.hasToken), "\(what) request without the Authorization header: \(matched.map(\.line))")
    }

    /// Dumps the raw request log — the evidence this file exists to produce.
    private func report(_ label: String, _ hits: [HTTPHit]) {
        for hit in hits { print("[hls-\(label)] \(hit.line)") }
    }

    private static func eventPlaylist(_ segments: Int) -> String {
        let body = (0..<segments)
            .map { String(format: "#EXTINF:2.000000,\nseg%05d.m4s", $0) }
            .joined(separator: "\n")
        return """
        #EXTM3U
        #EXT-X-VERSION:7
        #EXT-X-TARGETDURATION:2
        #EXT-X-MEDIA-SEQUENCE:0
        #EXT-X-PLAYLIST-TYPE:EVENT
        #EXT-X-MAP:URI="init.mp4"
        \(body)

        """
    }
}

// MARK: - Fixture

/// Builds a ~6s fMP4 HLS stream with the locally installed ffmpeg. Nothing is
/// committed: the clip is synthetic and lives in a temp directory for the run.
private enum HLSFixture {
    static let ffmpeg = "/opt/homebrew/bin/ffmpeg"

    static func make() throws -> URL {
        guard FileManager.default.isExecutableFile(atPath: ffmpeg) else {
            throw XCTSkip("ffmpeg not installed at \(ffmpeg) — cannot build an HLS fixture")
        }
        let dir = URL(fileURLWithPath: NSTemporaryDirectory())
            .appendingPathComponent("flimm-hls-\(UUID().uuidString)", isDirectory: true)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)

        let process = Process()
        process.executableURL = URL(fileURLWithPath: ffmpeg)
        process.currentDirectoryURL = dir
        process.arguments = arguments
        process.standardOutput = FileHandle.nullDevice
        process.standardError = FileHandle.nullDevice
        try process.run()
        process.waitUntilExit()
        guard process.terminationStatus == 0 else {
            throw XCTSkip("ffmpeg exited \(process.terminationStatus) — cannot build an HLS fixture")
        }
        return dir
    }

    /// `-g 30` forces a keyframe every 2s at 15fps; without it ffmpeg emits one
    /// long segment and the "several segments" assertion has nothing to see.
    private static let arguments = [
        "-y", "-loglevel", "error",
        "-f", "lavfi", "-i", "testsrc=size=320x240:rate=15:duration=6",
        "-f", "lavfi", "-i", "sine=frequency=440:duration=6",
        "-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
        "-g", "30", "-keyint_min", "30", "-sc_threshold", "0",
        "-c:a", "aac",
        "-f", "hls", "-hls_time", "2", "-hls_segment_type", "fmp4",
        "-hls_playlist_type", "vod", "-hls_fmp4_init_filename", "init.mp4",
        "-hls_segment_filename", "seg%05d.m4s", "index.m3u8"
    ]
}

// MARK: - Recording HTTP server

/// One request as the server saw it. `hasToken` is the whole experiment.
private struct HTTPHit {
    let method: String
    let path: String
    let headers: [String: String]

    var authorization: String? { headers["authorization"] }
    var hasToken: Bool { authorization == HLSHeaderForwardingTests.token }
    var line: String { "\(method) \(path) authorization=\(authorization ?? "<absent>")" }
}

private struct HTTPResponse {
    var status = 200
    var contentType: String
    var body: Data
    var extraHeaders: [String: String] = [:]
}

/// A ~150-line static file server on 127.0.0.1 that logs every request and the
/// headers it arrived with. No third-party dependency is permitted here, and
/// `URLProtocol` stubbing is useless because AVFoundation's HLS loader does not
/// go through `URLSession`, so this talks BSD sockets directly.
private final class RecordingHTTPServer: @unchecked Sendable {
    typealias Handler = @Sendable (HTTPHit) -> HTTPResponse?

    private let lock = NSLock()
    private var log: [HTTPHit] = []
    private var stopped = false
    private let listener: Int32
    private let root: URL
    private let handler: Handler?
    let port: UInt16

    init(root: URL, handler: Handler?) throws {
        self.root = root
        self.handler = handler
        let fd = socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { throw POSIXError(.EBADF) }

        var yes: Int32 = 1
        setsockopt(fd, SOL_SOCKET, SO_REUSEADDR, &yes, socklen_t(MemoryLayout<Int32>.size))
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = 0 // ephemeral
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        let bound = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bound == 0, Darwin.listen(fd, 16) == 0 else {
            close(fd)
            throw POSIXError(.EADDRINUSE)
        }
        listener = fd
        port = Self.assignedPort(of: fd)
        Thread.detachNewThread { [self] in acceptLoop() }
    }

    var recorded: [HTTPHit] { lock.withLock { log } }

    func stop() {
        lock.withLock { stopped = true }
        close(listener)
    }

    private var isStopped: Bool { lock.withLock { stopped } }

    private static func assignedPort(of fd: Int32) -> UInt16 {
        var address = sockaddr_in()
        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        _ = withUnsafeMutablePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(fd, $0, &length) }
        }
        return UInt16(bigEndian: address.sin_port)
    }

    private func acceptLoop() {
        while !isStopped {
            let client = accept(listener, nil, nil)
            guard client >= 0 else { continue }
            var yes: Int32 = 1
            setsockopt(client, SOL_SOCKET, SO_NOSIGPIPE, &yes, socklen_t(MemoryLayout<Int32>.size))
            var timeout = timeval(tv_sec: 10, tv_usec: 0)
            setsockopt(client, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
            // A thread per connection: AVFoundation opens several at once, and a
            // serialized accept loop would deadlock behind a slow client.
            Thread.detachNewThread { [self] in serve(client) }
        }
    }

    private func serve(_ client: Int32) {
        defer { close(client) }
        guard let head = Self.readHead(client), let hit = Self.parse(head) else { return }
        lock.withLock { log.append(hit) }
        let response = handler?(hit) ?? staticFile(for: hit)
        Self.send(response, to: client, headOnly: hit.method == "HEAD")
    }

    // MARK: Request parsing

    private static func readHead(_ fd: Int32) -> String? {
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 2048)
        let terminator = Data("\r\n\r\n".utf8)
        while data.range(of: terminator) == nil {
            let count = read(fd, &buffer, buffer.count)
            guard count > 0, data.count < 64 * 1024 else { return nil }
            data.append(buffer, count: count)
        }
        return String(decoding: data, as: UTF8.self)
    }

    private static func parse(_ head: String) -> HTTPHit? {
        var lines = head.components(separatedBy: "\r\n")
        let request = lines.removeFirst().split(separator: " ")
        guard request.count >= 2 else { return nil }
        var headers: [String: String] = [:]
        for line in lines where line.contains(":") {
            guard let separator = line.firstIndex(of: ":") else { continue }
            let name = line[line.startIndex..<separator].lowercased()
            headers[name] = line[line.index(after: separator)...].trimmingCharacters(in: .whitespaces)
        }
        return HTTPHit(method: String(request[0]), path: String(request[1]), headers: headers)
    }

    // MARK: Responses

    private func staticFile(for hit: HTTPHit) -> HTTPResponse {
        let name = String(hit.path.split(separator: "?").first?.dropFirst() ?? "")
        guard !name.isEmpty, !name.contains("/"),
              let data = try? Data(contentsOf: root.appendingPathComponent(name)) else {
            return HTTPResponse(status: 404, contentType: "text/plain", body: Data("not found".utf8))
        }
        return Self.ranged(data, contentType: Self.contentType(for: name), range: hit.headers["range"])
    }

    private static func contentType(for name: String) -> String {
        if name.hasSuffix(".m3u8") { return "application/vnd.apple.mpegurl" }
        if name.hasSuffix(".m4s") { return "video/iso.segment" }
        return "video/mp4"
    }

    /// AVFoundation asks for byte ranges of segments; answering 200 to a range
    /// request confuses it, so honour `Range` properly.
    private static func ranged(_ data: Data, contentType: String, range: String?) -> HTTPResponse {
        let whole = HTTPResponse(contentType: contentType, body: data)
        guard let range, range.hasPrefix("bytes=") else { return whole }
        let bounds = range.dropFirst("bytes=".count).split(separator: "-", omittingEmptySubsequences: false)
        let start = bounds.first.flatMap { Int($0) } ?? 0
        let end = (bounds.count > 1 ? Int(bounds[1]) : nil) ?? data.count - 1
        guard start <= end, end < data.count else { return whole }
        return HTTPResponse(
            status: 206,
            contentType: contentType,
            body: data.subdata(in: start..<(end + 1)),
            extraHeaders: ["Content-Range": "bytes \(start)-\(end)/\(data.count)"]
        )
    }

    private static func send(_ response: HTTPResponse, to fd: Int32, headOnly: Bool) {
        var head = "HTTP/1.1 \(response.status) \(reason(response.status))\r\n"
        head += "Content-Type: \(response.contentType)\r\n"
        head += "Content-Length: \(response.body.count)\r\n"
        head += "Accept-Ranges: bytes\r\n"
        for (name, value) in response.extraHeaders { head += "\(name): \(value)\r\n" }
        head += "Connection: close\r\n\r\n"
        writeAll(Data(head.utf8), to: fd)
        if !headOnly { writeAll(response.body, to: fd) }
    }

    private static func reason(_ status: Int) -> String {
        switch status {
        case 200: return "OK"
        case 206: return "Partial Content"
        case 404: return "Not Found"
        case 503: return "Service Unavailable"
        default: return "Unknown"
        }
    }

    private static func writeAll(_ data: Data, to fd: Int32) {
        data.withUnsafeBytes { raw in
            guard let base = raw.baseAddress else { return }
            var sent = 0
            while sent < raw.count {
                let count = write(fd, base + sent, raw.count - sent)
                guard count > 0 else { return }
                sent += count
            }
        }
    }
}

/// Lock-boxed counter — the connection threads and the notification callback
/// both need to bump one from outside the main thread.
private final class LockedCounter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0

    var value: Int { lock.withLock { count } }

    @discardableResult
    func next() -> Int { lock.withLock { count += 1; return count } }
}
