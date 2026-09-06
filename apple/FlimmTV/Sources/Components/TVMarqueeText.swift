import SwiftUI

/// A card title that fits stays put. One that does not fit its line limit is
/// cut off with an ellipsis until the card is focused, then reads out as a
/// single line scrolling slowly to the left — the tvOS convention for a
/// title too long for its card, and the only way to read the whole of one
/// from a sofa, where nothing can be tapped to expand it.
///
/// The clamped text keeps its place in the layout the whole time; the
/// scrolling line is an overlay on top of it. A card neither grows nor
/// shrinks when focus arrives, so the grid row it sits in stays where it was.
struct TVMarqueeText: View {
    let text: String
    let lineLimit: Int
    /// Whether the card is focused. Scrolling runs only while it is.
    let active: Bool

    /// Points per second: reading pace, not a ticker's.
    private static let speed: CGFloat = 70
    /// How long the start of the line is held before it moves, and the end
    /// before it snaps back — long enough to read either.
    private static let lead: Duration = .seconds(1)
    private static let hold: Duration = .seconds(1.5)
    private static let fade: CGFloat = 40

    @State private var clampedHeight: CGFloat = 0
    @State private var fullHeight: CGFloat = 0
    @State private var lineWidth: CGFloat = 0
    @State private var boxWidth: CGFloat = 0
    @State private var offset: CGFloat = 0
    @State private var scrolling = false

    private struct Run: Hashable {
        let on: Bool
        let distance: CGFloat
    }

    /// The limit cut something off: the same text laid out without one
    /// needs more height than the clamped copy was given.
    private var truncated: Bool { fullHeight > clampedHeight + 0.5 }

    private var run: Run {
        Run(on: active && truncated, distance: max(lineWidth - boxWidth, 0))
    }

    var body: some View {
        Text(text)
            .lineLimit(lineLimit)
            .multilineTextAlignment(.leading)
            .opacity(scrolling ? 0 : 1)
            .onGeometryChange(for: CGFloat.self, of: { $0.size.height }, action: { clampedHeight = $0 })
            .frame(maxWidth: .infinity, alignment: .leading)
            .onGeometryChange(for: CGFloat.self, of: { $0.size.width }, action: { boxWidth = $0 })
            // Two hidden probes: the text with no limit, wrapped at the box's
            // own width, to know whether the limit cut anything; and on one
            // line, to know how far that line has to travel. Neither takes
            // part in layout.
            .background {
                // Not before the box has a width: wrapped at zero, the probe
                // is a column of single letters and says every title is cut.
                if boxWidth > 0 {
                    Text(text)
                        .frame(width: boxWidth, alignment: .leading)
                        .fixedSize(horizontal: false, vertical: true)
                        .onGeometryChange(for: CGFloat.self, of: { $0.size.height }, action: { fullHeight = $0 })
                        .hidden()
                }
            }
            .background {
                Text(text)
                    .lineLimit(1)
                    .fixedSize()
                    .onGeometryChange(for: CGFloat.self, of: { $0.size.width }, action: { lineWidth = $0 })
                    .hidden()
            }
            .overlay(alignment: .topLeading) {
                if scrolling {
                    Text(text)
                        .lineLimit(1)
                        .fixedSize()
                        .offset(x: offset)
                }
            }
            .mask { edgeMask }
            .clipped()
            .task(id: run) { await scroll(run) }
    }

    /// While the line is moving, its trailing edge fades out instead of being
    /// cut mid-letter. Solid otherwise, so a clamped title is not dimmed.
    @ViewBuilder
    private var edgeMask: some View {
        if scrolling {
            HStack(spacing: 0) {
                Color.black
                LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing)
                    .frame(width: Self.fade)
            }
        } else {
            Color.black
        }
    }

    private func scroll(_ run: Run) async {
        var still = Transaction()
        still.disablesAnimations = true
        guard run.on, run.distance > 0 else {
            withTransaction(still) {
                scrolling = false
                offset = 0
            }
            return
        }
        scrolling = true
        let travel = Double(run.distance / Self.speed)
        do {
            while !Task.isCancelled {
                withTransaction(still) { offset = 0 }
                try await Task.sleep(for: Self.lead)
                withAnimation(.linear(duration: travel)) { offset = -run.distance }
                try await Task.sleep(for: .seconds(travel) + Self.hold)
            }
        } catch {
            // Cancelled: focus moved on, or the card was re-measured. The
            // task that replaces this one puts the title back.
        }
    }
}
