import XCTest
@testable import FlimmKit

/// Video quality is the one playback setting that is *not* a server
/// preference: it belongs to the screen and the network in front of it.
final class PlaybackSettingsTests: XCTestCase {
    private func defaults(_ name: String = #function) throws -> UserDefaults {
        let suite = "flimm.tests.\(name)"
        let defaults = try XCTUnwrap(UserDefaults(suiteName: suite))
        defaults.removePersistentDomain(forName: suite)
        return defaults
    }

    func testPreferencesRoundTripThroughTheirRawValue() {
        for preference in QualityPreference.options {
            XCTAssertEqual(QualityPreference(rawValue: preference.rawValue), preference)
        }
        XCTAssertEqual(QualityPreference.auto.rawValue, "auto")
        XCTAssertEqual(QualityPreference.height(1080).rawValue, "1080")
        XCTAssertNil(QualityPreference.auto.height)
        XCTAssertEqual(QualityPreference.height(720).height, 720)
    }

    /// Anything that is not a positive height is not a preference. A stored
    /// value from a future release must not decode as a nonsense height.
    func testNonsenseRawValuesAreRejected() {
        XCTAssertNil(QualityPreference(rawValue: "best"))
        XCTAssertNil(QualityPreference(rawValue: ""))
        XCTAssertNil(QualityPreference(rawValue: "0"))
        XCTAssertNil(QualityPreference(rawValue: "-720"))
    }

    /// Every rung the contract offers, tallest first, with Auto in front.
    func testTheOfferedOptions() {
        XCTAssertEqual(QualityPreference.options.first, .auto)
        XCTAssertEqual(QualityPreference.heights, [2160, 1440, 1080, 720, 480])
    }

    @MainActor
    func testAutoIsTheDefaultAndAChoiceIsRemembered() throws {
        let store = try defaults()
        XCTAssertEqual(PlaybackSettings(defaults: store).videoQuality, .auto)

        let settings = PlaybackSettings(defaults: store)
        settings.videoQuality = .height(720)
        XCTAssertEqual(store.string(forKey: PlaybackSettings.videoQualityKey), "720")
        // A fresh launch on the same device reads it back.
        XCTAssertEqual(PlaybackSettings(defaults: store).videoQuality, .height(720))
    }

    /// A key written by a newer build (or corrupted) falls back to Auto rather
    /// than refusing to start.
    @MainActor
    func testAnUnreadableStoredValueFallsBackToAuto() throws {
        let store = try defaults()
        store.set("ultra", forKey: PlaybackSettings.videoQualityKey)
        XCTAssertEqual(PlaybackSettings(defaults: store).videoQuality, .auto)
    }
}
