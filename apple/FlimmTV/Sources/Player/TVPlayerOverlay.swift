import FlimmKit
import SwiftUI

/// What is drawn over the video, in `contentOverlayView`.
///
/// Four things live here. **Subtitles**: Flimm's tracks are WebVTT sidecars
/// fetched with a bearer header, which `AVPlayerItem` has no way to attach, so
/// the cues are rendered rather than handed to the legible-media system — the
/// same choice the phone app makes, for the same reason. **Audio-only
/// artwork**: a music playlist has no video track, and a black rectangle is not
/// a player. **The compatible-rendition wait**: when this device cannot decode
/// what was archived the server transcodes on demand, and the part being
/// resumed from takes a few seconds — said out loud, with the encoder's own
/// progress, rather than left as a black screen.
///
/// **The end of the video**: a finished video is a still frame, which is
/// exactly what a paused one looks like, so the ending is said out loud along
/// with whatever plays next. It states rather than offers: the transport bar
/// underneath already holds previous/next and the scrubber, and a focusable
/// card here would have to take focus away from them. That is the one place
/// the TV differs from the phone, whose card carries its own Replay and
/// Up-next buttons.
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
            if model.hasEnded { ended }
            if let cue = model.activeCue, !cue.isEmpty {
                // Measured from the screen's edge, not from the safe area:
                // tvOS hands a hosting controller 60pt of overscan inset at
                // top and bottom, so a padding of 60 here landed the cue 120pt
                // up — a fifth of the way into the picture, which is what
                // "the subtitles are too high" was. The numbers below are what
                // reaches the panel, and they stay clear of overscan on their
                // own.
                //
                // 42 sits the cue about where a TV viewer expects it, low in
                // the frame without touching the edge. With the transport bar
                // up it has to clear the bar instead, which is a fixed lump of
                // AVKit's own and the reason that number is not simply double.
                VStack {
                    Spacer()
                    Text(cue)
                        .font(.system(size: subtitleSize, weight: .semibold))
                        .foregroundStyle(.white)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 26)
                        .padding(.vertical, 14)
                        .background(Palette.overlay, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
                        .padding(.bottom, transportBarVisible ? 300 : 42)
                        .frame(maxWidth: 1400)
                }
                .ignoresSafeArea()
                .animation(.easeOut(duration: 0.25), value: transportBarVisible)
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .allowsHitTesting(false)
    }

    private var ended: some View {
        ZStack {
            Color.black.opacity(0.7).ignoresSafeArea()
            VStack(spacing: 22) {
                Label("Finished", systemImage: "checkmark.circle")
                    .font(.title3.weight(.bold))
                    .foregroundStyle(.white.opacity(0.75))
                if let next = model.nextUp {
                    VStack(spacing: 10) {
                        Text("Up next")
                            .font(.caption.weight(.bold))
                            .foregroundStyle(.white.opacity(0.55))
                        Text(next.title)
                            .font(.title2.weight(.heavy))
                            .foregroundStyle(.white)
                            .lineLimit(2)
                        Text(next.channel.name)
                            .font(.body.weight(.semibold))
                            .foregroundStyle(.white.opacity(0.6))
                    }
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 1000)
                }
            }
        }
    }

    private var preparing: some View {
        ZStack {
            Color.black.opacity(0.8)
            // No spinner of our own — AVKit already spins over an item with
            // nothing to play yet. Its spinner is dead centre, so the words go
            // below it: far enough that the title clears the spinner, not so
            // far that they meet the transport bar. Short enough to fit that
            // band, which is why the explanation is two lines rather than four.
            VStack(spacing: 20) {
                Text(VideoQuality.preparingTitle(model.compatibleProgress))
                    .font(.title2.bold())
                Text("""
                This Apple TV can't decode the archived file, so the server is converting it, \
                starting where you left off.
                """)
                    .font(.title3)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
                    .frame(maxWidth: 900)
            }
            .offset(y: 190)
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
