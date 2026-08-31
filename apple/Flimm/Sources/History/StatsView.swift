import FlimmKit
import SwiftUI

/// What the viewer's history adds up to: hours, whose videos, and when.
///
/// Every number here is qualified on screen, because the table behind it holds
/// one row per video: "watched" is the summed furthest point reached rather
/// than a stopwatch, and the times are when a video was first *started*. See
/// `docs/api.md`, "Watch stats". The web client shows the same five things in
/// the same order.
struct StatsView: View {
    @Environment(AppModel.self) private var app
    @State private var range: StatsRange = .all
    @State private var stats: WatchStats?
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                Picker("Range", selection: $range) {
                    ForEach(StatsRange.allCases, id: \.self) { Text($0.label).tag($0) }
                }
                .pickerStyle(.segmented)

                if let error, stats == nil {
                    ErrorState(message: error, retry: { Task { await load() } })
                } else if let stats {
                    if stats.started == 0 {
                        EmptyState(
                            icon: "chart.bar",
                            title: "Nothing watched yet",
                            message: "Play something and this fills in — what you watched, whose videos, and when."
                        )
                    } else {
                        report(stats)
                    }
                } else {
                    ProgressView().frame(maxWidth: .infinity, alignment: .center)
                }
            }
            .padding(16)
        }
        .navigationTitle("Stats")
        .task(id: range) { await load() }
    }

    @ViewBuilder
    private func report(_ stats: WatchStats) -> some View {
        headlines(stats)
        if !stats.topChannels.isEmpty {
            StatsCard(title: "Whose videos") {
                ForEach(stats.topChannels) { channel in
                    ChannelBar(channel: channel, of: stats.topChannels.first?.seconds ?? 0)
                }
            }
        }
        StatsCard(title: "When you start watching", note: busiestHour(stats)) {
            StatsColumns(values: stats.byHour, label: { $0 % 6 == 0 ? "\($0)" : "" })
        }
        StatsCard(title: "Which days") {
            StatsColumns(values: stats.byWeekday, label: { Self.weekdays[$0] })
        }
        if !stats.byMonth.isEmpty {
            StatsCard(title: "Month by month") {
                StatsColumns(
                    values: stats.byMonth.map(\.videos),
                    label: { Self.monthLabel(stats.byMonth[$0].month) }
                )
            }
        }
        Text("""
        “Watched” is the furthest point reached in each video, added up: a finished video counts in full, an \
        abandoned one counts where it stopped, and watching something twice counts once. Times of day are when a \
        video was first started, in \(stats.zone).
        """)
        .font(.caption)
        .foregroundStyle(.secondary)
    }

    private func headlines(_ stats: WatchStats) -> some View {
        LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
            Headline(value: Fmt.durationLong(stats.seconds), label: "Watched")
            Headline(value: Fmt.compact(stats.started), label: "Videos started")
            Headline(value: Fmt.compact(stats.finished), label: "Finished")
            Headline(value: stats.finishRate.map { "\(Int(($0 * 100).rounded()))%" } ?? "—", label: "Finish rate")
        }
    }

    private func load() async {
        do {
            stats = try await app.client.stats(range: range)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }

    private func busiestHour(_ stats: WatchStats) -> String? {
        guard let peak = stats.byHour.max(), peak > 0, let hour = stats.byHour.firstIndex(of: peak) else { return nil }
        return "busiest at \(hour):00"
    }

    private static let weekdays = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]

    /// `2026-08` → `Aug`, with the year on January so a twelve-month row reads.
    static func monthLabel(_ month: String) -> String {
        let parts = month.split(separator: "-")
        guard parts.count == 2, let index = Int(parts[1]), (1...12).contains(index) else { return month }
        let short = DateFormatter().shortMonthSymbols?[index - 1] ?? String(parts[1])
        return index == 1 ? "\(short) \(parts[0].suffix(2))" : short
    }
}

/// A titled card, the shape every section here shares.
private struct StatsCard<Content: View>: View {
    let title: String
    var note: String?
    @ViewBuilder let content: Content

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Text(title).font(.subheadline.bold())
                Spacer(minLength: 8)
                if let note {
                    Text(note).font(.caption).foregroundStyle(.secondary)
                }
            }
            content
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

private struct Headline: View {
    let value: String
    let label: String

    var body: some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(value).font(.title2.bold())
            Text(label).font(.caption).foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(Palette.raised, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
    }
}

private struct ChannelBar: View {
    let channel: StatsChannel
    let of: Double

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Text(channel.name).font(.footnote.weight(.semibold)).lineLimit(1)
                Spacer(minLength: 8)
                Text("\(Fmt.durationLong(channel.seconds)) · \(channel.videos)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            GeometryReader { geo in
                let share = of > 0 ? channel.seconds / of : 0
                ZStack(alignment: .leading) {
                    Capsule().fill(.quaternary)
                    Capsule().fill(Palette.accent)
                        .frame(width: max(4, geo.size.width * share))
                }
            }
            .frame(height: 8)
        }
    }
}

/// A row of columns, heights relative to the busiest. A real-but-small value
/// keeps a visible stub: "one video" and "none" must not look the same.
private struct StatsColumns: View {
    let values: [Int]
    let label: (Int) -> String

    var body: some View {
        let peak = max(values.max() ?? 0, 1)
        HStack(alignment: .bottom, spacing: 3) {
            ForEach(Array(values.enumerated()), id: \.offset) { index, value in
                VStack(spacing: 4) {
                    GeometryReader { geo in
                        let height = value > 0 ? max(6, geo.size.height * Double(value) / Double(peak)) : 2
                        VStack {
                            Spacer(minLength: 0)
                            RoundedRectangle(cornerRadius: 3, style: .continuous)
                                .fill(value > 0 ? Palette.accent : Color.secondary.opacity(0.25))
                                .frame(height: height)
                        }
                        .frame(width: geo.size.width, height: geo.size.height)
                    }
                    Text(label(index))
                        .font(.system(size: 9))
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                .frame(maxWidth: 56)
            }
        }
        .frame(height: 96)
    }
}
