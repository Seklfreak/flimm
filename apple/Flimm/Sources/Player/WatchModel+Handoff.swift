import FlimmKit

/// What this session looks like to the device beside the phone.
///
/// It reads the same three things the heartbeat reports — which video, where in
/// it, in what run — so a continuation and a resume can never disagree about
/// where playback is.
extension WatchModel {
    /// `nil` until the video is loaded: an offer to continue something the
    /// phone cannot yet name is a banner with no title on it.
    var continuation: Continuation? {
        guard let video else { return nil }
        return Continuation(
            server: client.baseURL,
            destination: .watch(videoId: videoId, position: engine.currentTime, context: context),
            title: video.title
        )
    }
}
