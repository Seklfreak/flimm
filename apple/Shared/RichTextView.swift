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
        view.attributedText = attributed
        // Applied to every link range on top of the string's own attributes,
        // so the underline (https only) stays per run.
        view.linkTextAttributes = [.foregroundColor: UIColor(Palette.accent)]
        view.textContainer.maximumNumberOfLines = lineLimit ?? 0
    }

    func sizeThatFits(_ proposal: ProposedViewSize, uiView: UITextView, context: Context) -> CGSize? {
        // Take the width offered and answer with the height the text needs
        // at it; a text view left to itself wants one very long line.
        let width = proposal.width ?? UIView.layoutFittingExpandedSize.width
        let fitted = uiView.sizeThatFits(CGSize(width: width, height: .greatestFiniteMagnitude))
        return CGSize(width: proposal.width ?? fitted.width, height: fitted.height)
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
