import FlimmKit
import SwiftUI

/// The tab Flimm adds to `AVPlayerViewController`'s Info panel (swipe down on
/// the remote).
///
/// It carries the things AVKit has no concept of: "Start over" against the
/// server-held resume position, the shuffle seed, audio-only, and the four
/// preferences that change what playback does. Everything here writes through
/// `PATCH /me/prefs`, so a speed set from the sofa is the speed the phone uses
/// next.
struct TVPlayerInfoPanel: View {
    @Bindable var model: TVWatchModel

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 34) {
                header
                actions
                preferences
                if !model.upNext.isEmpty { upNext }
            }
            .padding(50)
        }
    }

    private var header: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(model.video?.title ?? "")
                .font(.title2.bold())
                .lineLimit(3)
            Text(subtitle)
                .font(.headline)
                .foregroundStyle(.secondary)
        }
    }

    private var subtitle: String {
        var parts: [String] = []
        if let channel = model.video?.channel.name { parts.append(channel) }
        if let nav = model.nav, !nav.isDetached {
            parts.append("\(nav.index + 1) of \(Fmt.count(nav.total))")
        }
        if model.isWatched { parts.append("seen") }
        // Worth saying: this is not the archived file, and which rendition it
        // is instead.
        if model.usingCompatibleRendition { parts.append(VideoQuality.renditionHint(model.activeVariant)) }
        return parts.joined(separator: " · ")
    }

    private var actions: some View {
        VStack(alignment: .leading, spacing: 16) {
            if let resumed = model.resumedFrom {
                Button {
                    Task { await model.startOver() }
                } label: {
                    Label("Start over (resumed from \(Fmt.duration(resumed)))", systemImage: "gobackward")
                }
            } else {
                Button {
                    Task { await model.startOver() }
                } label: {
                    Label("Start over", systemImage: "gobackward")
                }
            }

            Button {
                Task { await model.setWatched(!model.isWatched) }
            } label: {
                Label(
                    model.isWatched ? "Mark unseen" : "Mark seen",
                    systemImage: model.isWatched ? "circle" : "checkmark.circle"
                )
            }

            if model.hasContext {
                Button {
                    Task { await model.reshuffle() }
                } label: {
                    Label("Shuffle this list", systemImage: "shuffle")
                }
            }

            Button {
                Task { await model.toggleAudioOnly() }
            } label: {
                Label(
                    model.audioOnly ? "Play video" : "Audio only",
                    systemImage: model.audioOnly ? "play.rectangle" : "music.note"
                )
            }
        }
    }

    private var preferences: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Preferences")
                .font(.headline)
                .foregroundStyle(.secondary)

            Toggle("Autoplay next video", isOn: Binding(
                get: { model.prefs.autoplay },
                set: { value in Task { await model.setAutoplay(value) } }
            ))

            Toggle("Skip sponsor segments", isOn: Binding(
                get: { model.prefs.skipSponsors },
                set: { value in Task { await model.setSkipSponsors(value) } }
            ))

            TVOptionRow(title: "Speed", value: Fmt.speed(model.prefs.playbackSpeed)) {
                Task { await model.setSpeed(PlaybackSpeeds.next(after: model.prefs.playbackSpeed)) }
            }

            TVOptionRow(title: "Subtitles", value: subtitleLabel) {
                Task { await model.setSubtitleLanguage(nextSubtitleLanguage) }
            }

            if !model.audioOnly, !model.qualityLadder.isEmpty {
                TVOptionRow(title: "Quality", value: qualityLabel) {
                    Task { await model.setVideoQuality(nextQuality) }
                }
                Text(qualityFooter)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }
        }
    }

    /// `Auto` / `2160p · HEVC · preparing` — the current choice, with the
    /// rendition's state when there is something to say about it.
    private var qualityLabel: String {
        guard let height = model.videoQuality.height else { return VideoQuality.label(.auto) }
        guard let variant = model.qualityLadder.first(where: { $0.height == height }) else {
            return "\(height)p · not offered"
        }
        guard let hint = VideoQuality.stateHint(variant.state) else { return VideoQuality.label(variant) }
        return "\(VideoQuality.label(variant)) · \(hint)"
    }

    /// What Auto is doing right now, said in a line rather than left to be
    /// guessed at from a black screen.
    private var qualityFooter: String {
        guard model.videoQuality == .auto else { return "Changing quality restarts this video where you are." }
        if model.archivePlaysNatively { return "Auto is playing \(VideoQuality.sourceLabel(for: model.video).lowercased())." }
        guard let variant = model.activeVariant else { return "Auto picks the best rendition this screen can show." }
        return "Auto picked \(VideoQuality.label(variant)) for this screen."
    }

    /// Cycles Auto → the heights this video actually offers, tallest first. A
    /// picker is a poor fit inside the Info panel, and a ladder is at most five
    /// rungs long — the same reasoning as the speed and subtitle rows.
    private var nextQuality: QualityPreference {
        let options: [QualityPreference] = [.auto] + model.qualityLadder.map { .height($0.height) }
        guard let index = options.firstIndex(of: model.videoQuality) else { return options.first ?? .auto }
        return options[(index + 1) % options.count]
    }

    private var subtitleLabel: String {
        model.prefs.subtitleLang == Prefs.subtitlesOff ? "Off" : Fmt.langName(model.prefs.subtitleLang)
    }

    /// Cycles through the languages this video actually has, then "off" — a
    /// picker is a poor fit inside the Info panel and the archived track list
    /// is usually one or two entries long.
    private var nextSubtitleLanguage: String {
        let available = (model.video?.subtitles.map(\.lang) ?? []).reduced()
        let options = available + [Prefs.subtitlesOff]
        guard let index = options.firstIndex(of: model.prefs.subtitleLang) else { return options.first ?? Prefs.subtitlesOff }
        return options[(index + 1) % options.count]
    }

    private var upNext: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Up next")
                .font(.headline)
                .foregroundStyle(.secondary)
            ForEach(model.upNext.prefix(5)) { video in
                Button {
                    Task { await model.go(to: video.id) }
                } label: {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(video.title)
                            .lineLimit(1)
                        Text("\(video.channel.name) · \(Fmt.duration(video.duration))")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }
    }
}

/// A settings row that steps to the next value rather than opening a picker —
/// one click, which is the right cost for something you change mid-video.
struct TVOptionRow: View {
    let title: String
    let value: String
    let advance: () -> Void

    var body: some View {
        Button(action: advance) {
            HStack {
                Text(title)
                Spacer(minLength: 24)
                Text(value)
                    .foregroundStyle(.secondary)
            }
        }
    }
}

extension PlaybackSpeeds {
    /// Wraps, so the control is a single button.
    static func next(after speed: Double) -> Double {
        guard let index = all.firstIndex(of: speed) else { return 1.0 }
        return all[(index + 1) % all.count]
    }
}

private extension [String] {
    /// Distinct, in order — a video can carry the same language as both a user
    /// track and an auto one.
    func reduced() -> [String] {
        var seen = Set<String>()
        return filter { seen.insert($0).inserted }
    }
}
