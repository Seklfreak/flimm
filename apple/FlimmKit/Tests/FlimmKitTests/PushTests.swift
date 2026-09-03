import XCTest
@testable import FlimmKit

final class PushTests: XCTestCase {
    func testTokenHex() {
        XCTAssertEqual(DeviceToken.hex(Data([0x00, 0x0f, 0xab, 0xff])), "000fabff")
        XCTAssertEqual(DeviceToken.hex(Data()), "")
    }

    /// The server puts `feed` on every alert and `video` on a single-video
    /// one; that is the whole protocol.
    func testLinkFromNotification() {
        XCTAssertEqual(PushLink(userInfo: ["feed": "f1", "video": "v1", "aps": ["alert": "x"]]), .video(id: "v1", feedID: "f1"))
        XCTAssertEqual(PushLink(userInfo: ["feed": "f1"]), .feed(id: "f1"))
        XCTAssertEqual(PushLink(userInfo: ["feed": "f1", "video": ""]), .feed(id: "f1"))
        // A video with no feed is not something the server sends; nor is a
        // notification from anything that is not Flimm.
        XCTAssertNil(PushLink(userInfo: ["video": "v1"]))
        XCTAssertNil(PushLink(userInfo: ["aps": ["alert": "hello"]]))
        XCTAssertNil(PushLink(userInfo: [:]))
    }

    func testDeviceInputEncodesTheEnvironment() throws {
        let encoded = try FlimmCoding.encoder.encode(PushDeviceInput(platform: "ipados", environment: .sandbox))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        XCTAssertEqual(object["platform"] as? String, "ipados")
        XCTAssertEqual(object["environment"] as? String, "sandbox")
    }

    func testRegisterPutsTheToken() async throws {
        let session = StubURLProtocol.session { _, _ in (204, Data()) }
        let client = APIClient(baseURL: URL(string: "https://flimm.example.com")!, tokens: StaticTokenProvider("t"), session: session)
        try await client.registerDevice(token: "abc123", platform: "ios", environment: .production)
        let request = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(request.method, "PUT")
        XCTAssertEqual(request.path, "/api/v1/me/devices/abc123")
        XCTAssertEqual(request.header("Authorization"), "Bearer t")
        let body = try XCTUnwrap(request.body)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: body) as? [String: Any])
        XCTAssertEqual(object["environment"] as? String, "production")
        XCTAssertEqual(object["platform"] as? String, "ios")

        try await client.unregisterDevice(token: "abc123")
        let gone = try XCTUnwrap(StubURLProtocol.recorded.last)
        XCTAssertEqual(gone.method, "DELETE")
        XCTAssertEqual(gone.path, "/api/v1/me/devices/abc123")
    }
}
