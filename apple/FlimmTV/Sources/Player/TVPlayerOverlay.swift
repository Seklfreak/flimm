import FlimmKit
import SwiftUI

/// What is drawn over the video, in `contentOverlayView`.
///
/// Three things live here. **Subtitles**: Flimm's tracks are WebVTT sidecars
/// fetched with a bearer header, which `AVPlayerItem` has no way to attach, so
/// the cues are rendered rather than handed to the legible-media system — the
/// same choice the phone app makes, for the same reason. **Audio-only
/// artwork**: a music playlist has no video track, and a black rectangle is not
/// a player. **The compatible-rendition wait**: when this device cannot decode
/// what was archived the server transcodes on demand, and the part being
/// resumed from takes a few seconds — said out loud, with the encoder's own
/// progress, rather than left as a black screen.
///
/// It is deliberately inert: `allowsHitTesting(false)` keeps every remote press
/// going to the transport bar underneath.
struct TVPlayerOverlay: View {
    @Bindable var model: TVWatchModel
    /// AVKit's transport bar is up, so the captions have to get out of its
    /// way. Set from the player controller's delegate; see
    /// ``TVPlayerViewController``.
    var transportBarVisible = false

    var body: some View {
        ZStack {
            if model.audioOnly { artwork }
            if model.isPreparingCompatible { preparing }
            if let cue = model.activeCue, !cue.isEmpty {
                // Measured from the screen's edge, not from the safe area:
                // tvOS hands a hosting controller 60pt of overscan inset at
                // top and bottom, so a padding of 60 here landed the cue 120pt
                // up — a fifth of the way into the picture, which is what
                // "the subtitles are too high" was. The number below is what
                // reaches the panel, and it stays clear of overscan on its
                // own.
                VStack {
                    Spacer()
                    Text(cue)
                        .font(.system(size: subtitleSize, weight: .semibold))
                        .foregroundStyle(.white)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 26)
                        .padding(.vertical, 14)
                        .background(Palette.overlay, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .padding(.bottom, transportBarVisible ? 300 : 84)
                        .frame(maxWidth: 1400)
                }
                .ignoresSafeArea()
                .animation(.easeOut(duration: 0.25), value: transportBarVisible)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .allowsHitTesting(false)
    }

    private var preparing: some View {
        ZStack {
            Color.black.opacity(0.8)
            VStack(spacing: 24) {
                ProgressView()
                    .scaleEffect(1.6)
                Text(VideoQuality.preparingTitle(model.compatibleProgress))
                    .font(.title2.bold())
                Text("""
                This Apple TV can't decode the archived file, so the server is converting it. \
                It starts where you left off, so playback begins as soon as that part is ready.
                """)
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 900)
            }
        }
    }

    private var artwork: some View {
        ZStack {
            Color.black
            if let image = model.artwork {
                Image(uiImage: image)
                    .resizable()
                    .aspectRatio(contentMode: .fill)
                    .blur(radius: 60)
                    .opacity(0.4)
            }
            VStack(spacing: 26) {
                Group {
                    if let image = model.artwork {
                        Image(uiImage: image)
                            .resizable()
                            .aspectRatio(16 / 9, contentMode: .fill)
                    } else {
                        Palette.placeholder
                    }
                }
                .frame(width: 640, height: 360)
                .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
                .shadow(radius: 30)

                VStack(spacing: 8) {
                    Text(model.video?.title ?? "")
                        .font(.title.bold())
                        .lineLimit(2)
                    Text(model.video?.channel.name ?? "")
                        .font(.title3)
                        .foregroundStyle(.secondary)
                    Label("Audio only", systemImage: "music.note")
                        .font(.headline)
                        .foregroundStyle(.secondary)
                        .padding(.top, 4)
                }
                .multilineTextAlignment(.center)
                .frame(maxWidth: 900)
            }
        }
    }

    /// The server-held subtitle size preference, in points that read from a
    /// sofa rather than the phone's.
    private var subtitleSize: CGFloat {
        switch model.prefs.subtitleSize {
        case .small: 34
        case .medium: 42
        case .large: 52
        }
    }
}
