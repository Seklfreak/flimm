import Foundation
import Testing

@testable import FlimmKit

/// The server composes lists lazily and stops one item past the window it was
/// asked for, so `total` is a floor while more remains. Deciding "is there
/// another page" from the offset would end every list after its first page.
@Suite struct PageDecodingTests {
    private func page(_ json: String) throws -> Page<String> {
        try FlimmCoding.decoder.decode(Page<String>.self, from: Data(json.utf8))
    }

    @Test func takesHasMoreFromTheServer() throws {
        let p = try page(#"{"items":["a","b"],"page":0,"page_size":30,"total":3,"has_more":true}"#)
        #expect(p.hasMore)
        #expect(p.total == 3)
    }

    @Test func trustsHasMoreFalseOverTheCount() throws {
        // A page that looks unfinished by the old arithmetic but is not.
        let p = try page(#"{"items":["a"],"page":0,"page_size":1,"total":9,"has_more":false}"#)
        #expect(!p.hasMore)
    }

    @Test func fallsBackToTheCountWhenTheServerIsOlder() throws {
        #expect(try page(#"{"items":["a"],"page":0,"page_size":1,"total":9}"#).hasMore)
        #expect(try !page(#"{"items":["a"],"page":0,"page_size":1,"total":1}"#).hasMore)
    }
}
