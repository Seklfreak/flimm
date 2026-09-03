import XCTest
@testable import FlimmKit

final class StoredServerTests: XCTestCase {
    /// The server the app remembers is stored with a plain `JSONEncoder`
    /// (camelCase keys), and read back the same way — every field of the
    /// config has to survive that round trip, or a feature the server
    /// advertises is forgotten between launches.
    func testStoredServerKeepsPushEnabled() throws {
        let raw = #"""
        {"baseURL": "http://localhost:8080",
         "config": {"appName": "Flimm", "oidcIssuer": "", "oidcClientId": "", "version": "dev",
                    "authDisabled": true, "analyticsDisabled": false, "pushEnabled": true}}
        """#
        let server = try JSONDecoder().decode(FlimmServer.self, from: Data(raw.utf8))
        XCTAssertTrue(server.config.authDisabled)
        XCTAssertTrue(server.config.pushEnabled)
        let again = try JSONDecoder().decode(FlimmServer.self, from: JSONEncoder().encode(server))
        XCTAssertTrue(again.config.pushEnabled)
    }
}
