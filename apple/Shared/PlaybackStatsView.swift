import FlimmKit
import SwiftUI

/// The playback stats panel: what a player is actually doing.
///
/// One view for two readers. The phone draws it under its own player, and the
/// companion draws it under the television's transport from the readings the
/// Apple TV published — the same rows in the same order, because the whole use
/// of the panel is comparing one screen's answer with another's ("why is the
/// television transcoding this when the phone plays it directly?").
///
/// It sits **below** the picture, never over it, for the reason the web client
/// records: sixteen readings do not fit in a player box a few hundred points
/// tall, and the questions it answers get asked while looking at a page rather
/// than while filling a screen.
struct PlaybackStatsView: View {
    let stats: PlaybackStats
    /// Named so a companion can say whose readings these are. The phone's own
    /// panel passes nil.
    var device: String?
    var onClose: (() -> Void)?

    /// Debug door: `FLIMM_SHOW_STATS=1` opens the panel at launch, on the
    /// player and on the companion alike.
    ///
    ///     SIMCTL_CHILD_FLIMM_SHOW_STATS=1 xcrun simctl launch <device> …
    ///
    /// The panel is behind a menu item, and a simulator cannot open a menu.
    static var openAtLaunch: Bool {
        #if DEBUG
        ProcessInfo.processInfo.environment["FLIMM_SHOW_STATS"] == "1"
        #else
        false
        #endif
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text(device.map { "Playback stats · \($0)" } ?? "Playback stats")
                    .font(.caption.weight(.heavy))
                    .textCase(.uppercase)
                    .kerning(0.8)
                    .foregroundStyle(.secondary)
                Spacer(minLength: 8)
                if let onClose {
                    Button(action: onClose) {
                        Image(systemName: "xmark")
                            .font(.caption.weight(.bold))
                            .foregroundStyle(.secondary)
                            .frame(width: 30, height: 30)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Close playback stats")
                }
            }

            group("Delivery") {
                row("Path", stats.delivery.kind.label, strong: true)
                row("Why", stats.delivery.reason.sentence)
                row("Source", stats.delivery.source)
                if let rendition = stats.delivery.rendition {
                    row("Rendition", rendition.line)
                    if rendition.preparing {
                        row("Waiting on", "the first segment of the rendition")
                    }
                }
                row("URL", stats.delivery.url.isEmpty ? "—" : stats.delivery.url)
            }

            group("Derived") {
                row("Scrub preview", stats.derived.preview.line)
                row("Loudness", stats.derived.loudness.line)
            }

            group("Player") {
                row("State", stats.player.state)
                row("Picture", stats.player.picture)
                row("Buffer ahead", stats.player.bufferAhead.map { String(format: "%.1fs", $0) } ?? "—")
                if let dropped = PlaybackStats.dropped(stats.player.droppedFrames) {
                    row("Dropped frames", dropped)
                }
                if let bitrate = PlaybackStats.bitrate(stats.player.observedBitrate) {
                    row("Observed", bitrate)
                }
                row("Position", "\(Fmt.duration(stats.player.position)) of \(Fmt.duration(stats.player.duration))")
                row("Started at", stats.player.startedAt > 0 ? Fmt.duration(stats.player.startedAt) : "the beginning")
                row("Volume", "\(Int((stats.player.volume * 100).rounded()))%" + (stats.player.muted ? " (muted)" : ""))
            }

            group(device.map { "That screen: \($0)" } ?? "This device") {
                row("Decodes", stats.device.decoderList)
                row("Screen", "\(stats.device.screenHeight)px tall")
            }
        }
        .padding(14)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }

    @ViewBuilder
    private func group(_ title: String, @ViewBuilder rows: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 3) {
            Divider().padding(.vertical, 7)
            Text(title)
                .font(.caption2.weight(.heavy))
                .textCase(.uppercase)
                .kerning(0.7)
                .foregroundStyle(.tertiary)
                .padding(.bottom, 2)
            rows()
        }
    }

    /// Label and value on one line, with the labels aligned. The value wraps
    /// rather than truncating: a URL is one of the readings most worth having
    /// whole, and the panel is a page, not a status bar.
    private func row(_ label: String, _ value: String, strong: Bool = false) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Text(label)
                .foregroundStyle(.secondary)
                .frame(width: 96, alignment: .leading)
            Text(value)
                .fontWeight(strong ? .heavy : .semibold)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
        .font(.caption)
        .accessibilityElement(children: .combine)
    }
}
