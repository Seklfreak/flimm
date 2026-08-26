// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "FlimmKit",
    // macOS is listed only so `swift test` can run the suite natively on the
    // host; no macOS app is planned. iOS 17 / tvOS 17 are the shipping floors.
    platforms: [.iOS(.v17), .tvOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "FlimmKit", targets: ["FlimmKit"])
    ],
    targets: [
        .target(name: "FlimmKit"),
        .testTarget(name: "FlimmKitTests", dependencies: ["FlimmKit"])
    ]
)
