import FlimmKit
import SwiftUI
import UIKit

/// Loads `/media/thumb/…` with the `Authorization: Bearer` header and keeps the
/// result in memory.
///
/// `AsyncImage` cannot help here: it has no way to set a header, and every
/// media path on this backend is authenticated. Thumbnails come back with
/// `Cache-Control: private, max-age=86400`, so `URLSession`'s shared cache does
/// the persistence and this store only avoids re-decoding.
@MainActor
final class MediaImageStore {
    static let shared = MediaImageStore()

    private let cache: NSCache<NSString, UIImage> = {
        let cache = NSCache<NSString, UIImage>()
        cache.totalCostLimit = 64 * 1024 * 1024
        return cache
    }()
    private var inFlight: [String: Task<UIImage?, Never>] = [:]

    func cached(_ path: String) -> UIImage? {
        cache.object(forKey: path as NSString)
    }

    func image(at path: String, client: APIClient) async -> UIImage? {
        if let hit = cached(path) { return hit }
        if let running = inFlight[path] { return await running.value }

        let task = Task { [weak self] () -> UIImage? in
            guard let url = client.mediaURL(path) else { return nil }
            let headers = (try? await client.mediaHeaders()) ?? [:]
            var request = URLRequest(url: url)
            for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
            let image = await Self.fetch(request)
            if let image { self?.store(image, for: path) }
            return image
        }
        inFlight[path] = task
        let image = await task.value
        inFlight[path] = nil
        return image
    }

    private func store(_ image: UIImage, for path: String) {
        let cost = Int(image.size.width * image.size.height * image.scale * image.scale * 4)
        cache.setObject(image, forKey: path as NSString, cost: cost)
    }

    /// Off the main actor: the decode is the expensive half.
    private nonisolated static func fetch(_ request: URLRequest) async -> UIImage? {
        guard let (data, response) = try? await URLSession.shared.data(for: request),
              let http = response as? HTTPURLResponse, (200..<300).contains(http.statusCode) else {
            return nil
        }
        return UIImage(data: data)
    }
}

/// A thumbnail from the backend's media proxy.
struct MediaImage: View {
    let path: String?
    var contentMode: ContentMode = .fill

    @Environment(AppModel.self) private var app
    @State private var image: UIImage?

    var body: some View {
        ZStack {
            Palette.placeholder
            if let image {
                Image(uiImage: image)
                    .resizable()
                    .aspectRatio(contentMode: contentMode)
                    .transition(.opacity)
            }
        }
        .task(id: path) { await load() }
    }

    private func load() async {
        guard let path, !path.isEmpty else { return }
        if let hit = MediaImageStore.shared.cached(path) {
            image = hit
            return
        }
        let loaded = await MediaImageStore.shared.image(at: path, client: app.client)
        guard !Task.isCancelled else { return }
        withAnimation(.easeOut(duration: 0.15)) { image = loaded }
    }
}

/// Circular channel avatar with the channel's initial as the fallback.
struct ChannelAvatar: View {
    let path: String?
    let name: String
    var size: CGFloat = 40

    var body: some View {
        MediaImage(path: path)
            .frame(width: size, height: size)
            .clipShape(Circle())
            .overlay {
                if path?.isEmpty != false {
                    Text(String(name.prefix(1)).uppercased())
                        .font(.system(size: size * 0.42, weight: .bold))
                        .foregroundStyle(.secondary)
                }
            }
    }
}
