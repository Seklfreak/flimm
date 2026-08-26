import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI
import UIKit

/// A QR code for the device-flow verification URL.
///
/// CoreImage renders it at roughly one pixel per module, which is a ~25pt
/// image; scaling that up has to be nearest-neighbour or the edges blur into
/// something a camera will not read. `CGAffineTransform` on the CoreImage
/// output does exactly that, so the scale-up happens before rasterising.
enum QRCode {
    static func image(for text: String, side: CGFloat) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        // "M" recovers ~15% — enough for a screen, and it keeps the modules
        // large enough to scan from a sofa.
        filter.correctionLevel = "M"
        guard let output = filter.outputImage else { return nil }

        let scale = side / output.extent.width
        let scaled = output.transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        let context = CIContext()
        guard let cgImage = context.createCGImage(scaled, from: scaled.extent) else { return nil }
        return UIImage(cgImage: cgImage)
    }
}

/// The code itself, on white so a camera has the contrast it expects however
/// the rest of the screen is themed.
struct QRCodeView: View {
    let text: String
    var side: CGFloat = 320

    var body: some View {
        Group {
            if let image = QRCode.image(for: text, side: side * 2) {
                Image(uiImage: image)
                    .resizable()
                    .interpolation(.none)
                    .frame(width: side, height: side)
            } else {
                RoundedRectangle(cornerRadius: 12, style: .continuous)
                    .fill(Palette.placeholder)
                    .frame(width: side, height: side)
            }
        }
        .padding(16)
        .background(Color.white, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        .accessibilityHidden(true)
    }
}
