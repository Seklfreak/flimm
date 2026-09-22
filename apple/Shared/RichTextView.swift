import FlimmKit
import SwiftUI

#if os(iOS)
/// A description or a comment as it reads: URLs open in the browser,
/// timestamps seek, everything else is the text as written. What counts as
/// either is ``FlimmKit/RichText``'s decision, shared with the web; this view
/// only draws it.
///
/// A `UITextView` rather than a SwiftUI `Text` with link attributes. The
/// `Text` looked the same and was wrong to touch: once a link wrapped onto a
/// second line, a tap *anywhere* in that paragraph opened it — the first
/// line, the plain sentence after it, all of it — which for a comment is a
/// text that throws you into Safari. UIKit's link hit-testing is exact,
/// selection comes with it, and the delegate is where a timestamp's
/// `flimm-seek:` URL becomes a seek instead of a hand-off to Safari.
struct RichTextView: UIViewRepresentable {
    let text: String
    /// The video's length, so a timestamp past the end stays text.
    var duration: Double?
    /// Without it timestamps are text too: nothing here can seek.
    var onSeek: ((Double) -> Void)?
    var style: UIFont.TextStyle = .body
    var color: UIColor = .label
    /// Lines before truncation; nil shows everything. A parameter rather
    /// than `.lineLimit`, which does not reach into a UIKit view.
    var lineLimit: Int?

    func makeUIView(context: Context) -> UITextView {
        let view = UITextView()
        view.isEditable = false
        view.isScrollEnabled = false
        view.isSelectable = true
        view.backgroundColor = .clear
        view.textContainerInset = .zero
        view.textContainer.lineFragmentPadding = 0
        view.textContainer.lineBreakMode = .byTruncatingTail
        view.adjustsFontForContentSizeCategory = true
        view.dataDetectorTypes = []
        view.delegate = context.coordinator
        // Wrap rather than push the column wider.
        view.setContentCompressionResistancePriority(.defaultLow, for: .horizontal)
        view.setContentHuggingPriority(.defaultLow, for: .horizontal)
        return view
    }

    func updateUIView(_ view: UITextView, context: Context) {
        context.coordinator.onSeek = onSeek
        // Only when it actually changed. Assigning `attributedText` throws
        // TextKit's layout away, and SwiftUI calls `updateUIView` for
        // reasons that have nothing to do with the text — so an
        // unconditional assignment makes every following measurement lay
        // the whole comment out again from nothing. Comparing two
        // attributed strings is a fraction of laying one out.
        let next = attributed
        // Compared against what we last set, not against `view.attributedText`:
        // a text view normalises what it is given, so reading it back can
        // never equal what we built and the check would never hold.
        if context.coordinator.assigned != next {
            view.attributedText = next
            context.coordinator.assigned = next
        }
        // Applied to every link range on top of the string's own attributes,
        // so the underline (https only) stays per run.
        let linkAttributes: [NSAttributedString.Key: Any] = [.foregroundColor: UIColor(Palette.accent)]
        if view.linkTextAttributes?[.foregroundColor] as? UIColor != linkAttributes[.foregroundColor] as? UIColor {
            view.linkTextAttributes = linkAttributes
        }
        let lines = lineLimit ?? 0
        if view.textContainer.maximumNumberOfLines != lines {
            view.textContainer.maximumNumberOfLines = lines
        }
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView: UITextView, context: Context) -> CGSize? {
        // Take the width offered and answer with the height the text needs
        // at it; a text view left to itself wants one very long line.
        let width = proposal.width ?? UIView.layoutFittingExpandedSize.width
        // Measured once per width, not once per ask. A UITextView sizes
        // itself by running a full TextKit2 layout — glyph advances and all
        // — and SwiftUI asks repeatedly: a stack resolves its alignment,
        // resizes, and sizes its children ideally, each pass measuring
        // every child again, and comment lists nest those stacks several
        // deep with one of these views per comment. FLIMM-IOS-3 was 7
        // seconds of exactly that on an iPad. The answers are identical, so
        // only the first one is paid for.
        guard let offered = proposal.width else {
            // Nothing offered, so the text's own width is the answer and
            // there is no width to key a cached height on.
            let fitted = uiView.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude))
            return CGSize(width: fitted.width, height: fitted.height)
        }
        let height = context.coordinator.height(
            for: context.coordinator.assigned,
            lineLimit: uiView.textContainer.maximumNumberOfLines,
            width: width
        ) {
            uiView.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude)).height
        }
        return CGSize(width: offered, height: height)
    }

    func makeCoordinator() -> Coordinator { Coordinator() }

    private var attributed: NSAttributedString {
        let font = UIFont.preferredFont(forTextStyle: style)
        let bold = UIFont(
            descriptor: font.fontDescriptor.withSymbolicTraits(.traitBold) ?? font.fontDescriptor,
            size: 0
        )
        let out = NSMutableAttributedString()
        for segment in RichText.segments(text, duration: duration) {
            switch segment {
            case .text(let s):
                out.append(NSAttributedString(string: s, attributes: [.font: font, .foregroundColor: color]))
            case .link(let s, let url):
                out.append(NSAttributedString(string: s, attributes: [
                    .font: font, .link: url, .underlineStyle: NSUnderlineStyle.single.rawValue,
                ]))
            case .time(let s, let seconds):
                guard onSeek != nil else {
                    out.append(NSAttributedString(string: s, attributes: [.font: font, .foregroundColor: color]))
                    continue
                }
                out.append(NSAttributedString(string: s, attributes: [
                    .font: bold, .link: RichText.seekURL(seconds),
                    .backgroundColor: UIColor(Palette.accent).withAlphaComponent(0.12),
                ]))
            }
        }
        return out
    }

    final class Coordinator: NSObject, UITextViewDelegate {
        var onSeek: ((Double) -> Void)?

        /// The string last handed to the text view. Kept here because a
        /// text view normalises what it is given, so it cannot answer
        /// "is this still what you are showing?" about the string we built.
        var assigned: NSAttributedString?

        /// Heights already measured for `measured`, by proposed width.
        /// Dropped whenever the text or the line limit changes, which is
        /// the only time a previous answer could be wrong — Dynamic Type
        /// included, since a new type size rebuilds the attributed string
        /// with a different font and so fails the equality check.
        private var heights: [CGFloat: CGFloat] = [:]
        private var measured: NSAttributedString?
        private var measuredLineLimit = 0

        func height(
            for text: NSAttributedString?,
            lineLimit: Int,
            width: CGFloat,
            measure: () -> CGFloat
        ) -> CGFloat {
            if measured != text || measuredLineLimit != lineLimit {
                measured = text
                measuredLineLimit = lineLimit
                heights.removeAll(keepingCapacity: true)
            }
            // Half-point buckets: SwiftUI offers the same column width back
            // as a value that can differ in the last bits, and a cache that
            // misses on that is not a cache.
            let key = (width * 2).rounded() / 2
            if let known = heights[key] { return known }
            let height = measure()
            heights[key] = height
            return height
        }

        func textView(_ textView: UITextView, primaryActionFor textItem: UITextItem, defaultAction: UIAction) -> UIAction? {
            guard case .link(let url) = textItem.content, let seconds = RichText.seekSeconds(url) else {
                return defaultAction
            }
            return UIAction { [weak self] _ in self?.onSeek?(seconds) }
        }

        /// No "Open Link / Copy" menu for a timestamp: there is no page
        /// behind it, and the URL is not one anybody can use.
        func textView(
            _ textView: UITextView, menuConfigurationFor textItem: UITextItem, defaultMenu: UIMenu
        ) -> UITextItem.MenuConfiguration? {
            if case .link(let url) = textItem.content, RichText.seekSeconds(url) != nil { return nil }
            return .init(menu: defaultMenu)
        }
    }
}
#endif
