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
/// what was archived the server transcodes on demand, and the first segment
/// takes a few seconds — said out loud rather than left as a black screen.
///
/// It is deliberately inert: `allowsHitTesting(false)` keeps every remote press
/// going to the transport bar underneath.
struct TVPlayerOverlay: View {
    @Bindable var model: TVWatchModel

    var body: some View {
        ZStack {
            if model.audioOnly { artwork }
            if model.isPreparingCompatible { preparing }
            if let cue = model.activeCue, !cue.isEmpty {
                VStack {
                    Spacer()
                    Text(cue)
                        .font(.system(size: subtitleSize, weight: .semibold))
                        .foregroundStyle(.white)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 26)
                        .padding(.vertical, 14)
                        .background(Palette.overlay, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .padding(.bottom, 120)
                        .frame(maxWidth: 1400)
                }
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
                Text("Preparing a compatible version…")
                    .font(.title2.bold())
                Text("""
                This Apple TV can't decode the archived file, so the server is converting it. \
                Playback starts as soon as the first part is ready.
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
