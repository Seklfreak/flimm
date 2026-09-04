import FlimmKit
import SwiftUI

/// The tab Flimm adds to `AVPlayerViewController`'s Info panel (swipe down on
/// the remote).
///
/// It carries the things AVKit has no concept of: "Start over" against the
/// server-held resume position, the shuffle seed, audio-only, and the
/// preferences that change what playback does. Everything here writes through
/// `PATCH /me/prefs`, so a speed set from the sofa is the speed the phone uses
/// next.
///
/// **It has to fit the strip AVKit gives it.** The panel is a short band
/// across the top of the screen, not a screen of its own, and a tall stack
/// scrolled inside it shows a few rows clipped mid-height with the rest of the
/// content — and the background — running off the edge. So the layout is two
/// columns that fit without scrolling: what you can *do* on the left, what you
/// can *change* on the right, one line of context above them. Anything that
/// cannot be made to fit does not belong here (which is why "Up next" is not:
/// autoplay already decides what follows, and the transport bar can step).
struct TVPlayerInfoPanel: View {
    /// Shared with the blurred ground behind this, which has to line up with
    /// it exactly. See `TVPlayerViewController.dressInfoPanel`.
    static let groundRadius: CGFloat = 28
    static let groundInset: CGFloat = 12

    @Bindable var model: TVWatchModel

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            header
            HStack(alignment: .top, spacing: 60) {
                actions
                    .frame(maxWidth: .infinity, alignment: .leading)
                preferences
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, 40)
        .padding(.vertical, 28)
        // Fill whatever AVKit hands over — or rows sit on moving picture
        // wherever the content stops short — and carry a translucent ground of
        // its own: enough to read against on its own, with the blur behind it
        // (`TVPlayerViewController.dressInfoPanel`) doing the rest. Drawn here
        // rather than only in UIKit so the panel is never left transparent if
        // AVKit rebuilds its hierarchy, and as a `background` rather than a
        // clip, because tvOS grows a focused row past its bounds and clipping
        // would cut it.
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .background(
            Color.black.opacity(0.35),
            in: RoundedRectangle(cornerRadius: TVPlayerInfoPanel.groundRadius, style: .continuous)
        )
        .padding(.horizontal, TVPlayerInfoPanel.groundInset)
    }

    private var header: some View {
        HStack(alignment: .firstTextBaseline, spacing: 16) {
            Text(model.video?.title ?? "")
                .font(.title3.bold())
                .lineLimit(1)
            Text(subtitle)
                .font(.subheadline)
                .foregroundStyle(.secondary)
                .lineLimit(1)
            votes
            Spacer(minLength: 0)
        }
    }

    /// The view and vote counts, exactly as the phone shows them. Counts, not
    /// controls — the remote cannot vote on YouTube's behalf — and the dislike
    /// half is there only when the deployment enabled Return YouTube Dislike
    /// and that service knows the video (docs/api.md, "Views and votes").
    @ViewBuilder
    private var votes: some View {
        if let stats = model.video?.stats, stats.views > 0 || stats.likes > 0 || stats.dislikes != nil {
            HStack(spacing: 14) {
                if stats.views > 0 {
                    Text("\(Fmt.compact(stats.views)) views")
                }
                // See the phone: no thumb rather than a thumb reading zero.
                if stats.likes > 0 || stats.dislikes != nil {
                    Label(Fmt.compact(stats.likes), systemImage: "hand.thumbsup")
                }
                if let dislikes = stats.dislikes {
                    Label(Fmt.compact(dislikes), systemImage: "hand.thumbsdown")
                }
            }
            .font(.subheadline)
            .foregroundStyle(.secondary)
            .lineLimit(1)
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
        VStack(alignment: .leading, spacing: 10) {
            // A category set to "ask" is offered here rather than as an
            // overlay button: the overlay takes no focus (the remote's presses
            // belong to the transport bar), and this panel is where the TV
            // puts everything the remote's own buttons do not do.
            if let offer = SponsorRules.segmentToOffer(
                at: model.currentTime, in: model.video?.sponsorblock ?? [], prefs: model.prefs
            ) {
                Button {
                    model.seek(to: offer.end)
                } label: {
                    Label("Skip \(SponsorRules.label(offer.category).lowercased())", systemImage: "forward.end.fill")
                }
            }

            // A point of interest is offered, never taken: the remote has no
            // scrubber worth hunting on, so this is where the TV gets it.
            if let highlight = SponsorRules.highlightToOffer(
                at: model.currentTime, in: model.video?.sponsorblock ?? []
            ) {
                Button {
                    model.seek(to: highlight.start)
                } label: {
                    Label("Jump to the highlight (\(Fmt.duration(highlight.start)))", systemImage: "sparkles")
                }
            }

            // Also on the transport bar; here too, because this panel is
            // where a viewer looks for what the remote's buttons do not do.
            if model.canGoPrevious {
                Button {
                    Task { await model.goPrevious() }
                } label: {
                    Label("Previous video", systemImage: "backward.end.fill")
                }
            }

            if model.canGoNext {
                Button {
                    Task { await model.goNext() }
                } label: {
                    Label("Next video", systemImage: "forward.end.fill")
                }
            }

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

            Button {
                Task { await model.setDismissed(!model.isDismissed) }
            } label: {
                Label(
                    model.isDismissed ? "Add back to feeds" : "Not interested",
                    systemImage: model.isDismissed ? "arrow.uturn.backward" : "hand.thumbsdown"
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
        VStack(alignment: .leading, spacing: 10) {
            Text("Preferences")
                .font(.caption)
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
                // The footnote that used to sit here is gone: in a band this
                // short a paragraph costs two rows and pushes something you
                // can act on off the panel. What Auto is doing is in the value.
                TVOptionRow(title: "Quality", value: qualityLabel) {
                    Task { await model.setVideoQuality(nextQuality) }
                }
            }
        }
    }

    /// `Auto · 1080p` / `2160p · HEVC · preparing` — the current choice, and
    /// what it resolved to. Auto says what it actually picked because a band
    /// this short has no room for the sentence that used to explain it, and
    /// "Auto" alone tells a viewer staring at a soft picture nothing.
    private var qualityLabel: String {
        guard let height = model.videoQuality.height else {
            if model.archivePlaysNatively {
                return "\(VideoQuality.label(.auto)) · \(VideoQuality.sourceLabel(for: model.video))"
            }
            guard let variant = model.activeVariant else { return VideoQuality.label(.auto) }
            return "\(VideoQuality.label(.auto)) · \(VideoQuality.label(variant))"
        }
        guard let variant = model.qualityLadder.first(where: { $0.height == height }) else {
            return "\(height)p · not offered"
        }
        guard let hint = VideoQuality.stateHint(variant.state) else { return VideoQuality.label(variant) }
        return "\(VideoQuality.label(variant)) · \(hint)"
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
