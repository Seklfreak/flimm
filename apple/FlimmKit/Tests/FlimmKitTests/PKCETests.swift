import CryptoKit
import XCTest
@testable import FlimmKit

final class PKCETests: XCTestCase {
    /// RFC 7636 Appendix B — the canonical verifier/challenge pair. If this
    /// fails, every sign-in fails with `invalid_grant` and the reason is
    /// invisible from the client side.
    func testChallengeMatchesRFC7636TestVector() {
        let pkce = PKCE(verifier: "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")
        XCTAssertEqual(pkce.challenge, "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
        XCTAssertEqual(PKCE.method, "S256")
    }

    func testChallengeIsBase64URLWithoutPadding() {
        let challenge = PKCE().challenge
        XCTAssertFalse(challenge.contains("+"))
        XCTAssertFalse(challenge.contains("/"))
        XCTAssertFalse(challenge.contains("="))
        // SHA-256 is 32 bytes → 43 base64 characters once the padding is gone.
        XCTAssertEqual(challenge.count, 43)
    }

    func testVerifierIsUnreservedAndLongEnough() {
        let unreserved = CharacterSet(charactersIn: "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~")
        for _ in 0..<20 {
            let verifier = PKCE().verifier
            XCTAssertTrue((43...128).contains(verifier.count), "length \(verifier.count)")
            XCTAssertTrue(verifier.unicodeScalars.allSatisfy(unreserved.contains), verifier)
        }
    }

    func testVerifiersAreNotRepeated() {
        let verifiers = Set((0..<50).map { _ in PKCE().verifier })
        XCTAssertEqual(verifiers.count, 50)
    }

    func testStateIsRandomAndURLSafe() {
        let first = PKCE.randomState()
        XCTAssertNotEqual(first, PKCE.randomState())
        XCTAssertFalse(first.contains("+"))
        XCTAssertFalse(first.contains("/"))
        XCTAssertFalse(first.contains("="))
    }
}
