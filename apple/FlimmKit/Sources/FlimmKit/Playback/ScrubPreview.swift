import CoreGraphics
import Foundation

/// Scrub previews: the still that appears while a viewer drags the scrubber.
///
/// The server derives one sprite sheet per video plus a WebVTT track saying
/// which tile covers which seconds (`sheet.jpg#xywh=x,y,w,h`) — the same pair
/// the web player uses, parsed here rather than handed to `AVPlayer`, which has
/// no way to side-load a track that needs the bearer header and would give us
/// cue callbacks when all a drag needs is "which rectangle for this second".

/// One still of the sheet: the rectangle to draw, and the stretch of video it
/// stands for.
public struct PreviewTile: Sendable, Hashable {
    public let start: Double
    public let end: Double
    /// The sheet's media path, resolved against the track's own path — the
    /// track says `sheet.jpg`, and the sheet is not next to whatever the app
    /// last loaded.
    public let sheetPath: String
    /// Where the still sits in the sheet, in the sheet's own pixels.
    public let rect: CGRect

    public init(start: Double, end: Double, sheetPath: String, rect: CGRect) {
        self.start = start
        self.end = end
        self.sheetPath = sheetPath
        self.rect = rect
    }
}

public enum ScrubPreview {
    /// Reads a preview track. Anything malformed is dropped rather than
    /// thrown: a scrubber with no stills is still a scrubber, and a half-built
    /// derivation must not take the player down with it.
    public static func tiles(from vtt: String, trackPath: String) -> [PreviewTile] {
        WebVTT.parse(vtt).compactMap { cue in
            let payload = cue.text.trimmingCharacters(in: .whitespacesAndNewlines)
            guard let hash = payload.range(of: "#xywh=") else { return nil }
            let numbers = payload[hash.upperBound...].split(separator: ",").map { Double($0) }
            guard numbers.count == 4, let x = numbers[0], let y = numbers[1],
                  let width = numbers[2], let height = numbers[3],
                  width > 0, height > 0 else {
                return nil
            }
            return PreviewTile(
                start: cue.start,
                end: cue.end,
                sheetPath: resolve(String(payload[payload.startIndex..<hash.lowerBound]), against: trackPath),
                rect: CGRect(x: x, y: y, width: width, height: height)
            )
        }
    }

    /// The tile covering `time`, or the last one before it — a drag past the
    /// final cue holds the last still rather than going blank.
    public static func tile(at time: Double, in tiles: [PreviewTile]) -> PreviewTile? {
        if let covering = tiles.last(where: { $0.start <= time && time < $0.end }) { return covering }
        if let before = tiles.last(where: { $0.start <= time }) { return before }
        return tiles.first
    }

    /// Fetches a video's preview track, waiting out the derivation.
    ///
    /// A 404 is the normal first answer — asking is what starts the work — so
    /// this asks again with growing gaps for as long as the caller's task
    /// lives. It used to stop after three waits, which meant a sheet that took
    /// longer than three quarters of a minute to derive never reached the
    /// scrubber it was made for, however long the video stayed on screen.
    /// Nothing waits on this either way; the video is already playing.
    public static func load(trackPath: String, client: APIClient) async -> [PreviewTile] {
        var attempt = 0
        while !Task.isCancelled {
            if attempt > 0 {
                let gap = retryGaps[min(attempt, retryGaps.count - 1)]
                guard (try? await Task.sleep(nanoseconds: UInt64(gap * 1_000_000_000))) != nil else { return [] }
            }
            if let vtt = await fetch(trackPath, client: client) {
                return tiles(from: vtt, trackPath: trackPath)
            }
            attempt += 1
        }
        return []
    }

    /// The first entry is the first try, so only the later ones are waits. A
    /// sheet is one decode of the whole file, which is why they grow; the last
    /// gap is then held until the player closes.
    private static let retryGaps: [Double] = [0, 4, 10, 30, 60]

    private static func fetch(_ path: String, client: APIClient) async -> String? {
        guard let url = client.mediaURL(path), let headers = try? await client.mediaHeaders() else { return nil }
        var request = URLRequest(url: url)
        for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            return nil
        }
        return String(data: data, encoding: .utf8)
    }

    /// `sheet.jpg` next to `/media/preview/{id}/preview.vtt`. An absolute
    /// payload is taken as it is, and an empty one has nothing to resolve.
    private static func resolve(_ name: String, against trackPath: String) -> String {
        if name.isEmpty { return trackPath }
        if name.hasPrefix("/") { return name }
        let directory = trackPath.split(separator: "/", omittingEmptySubsequences: false).dropLast().joined(separator: "/")
        return directory.isEmpty ? name : directory + "/" + name
    }
}
