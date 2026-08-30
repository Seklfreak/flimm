import XCTest
@testable import FlimmKit

final class CommentTests: XCTestCase {
    private let decoder = FlimmCoding.decoder

    func testACommentDecodesTheContractsShape() throws {
        let json = """
        {
          "id": "c1",
          "author": "@someone",
          "author_id": "UC-someone",
          "text": "Worth the wait.",
          "likes": 128,
          "published": "2026-08-20T09:00:00Z",
          "time_text": "1 week ago",
          "hearted": true,
          "from_uploader": false,
          "replies": [
            {"id": "c1r1", "author": "@the-workshop", "text": "Thanks!", "likes": 9,
             "published": null, "time_text": "", "hearted": false, "from_uploader": true, "replies": []}
          ]
        }
        """
        let comment = try decoder.decode(VideoComment.self, from: Data(json.utf8))
        XCTAssertEqual(comment.id, "c1")
        XCTAssertEqual(comment.authorID, "UC-someone", "author_id must not decode as empty")
        XCTAssertEqual(comment.likes, 128)
        XCTAssertTrue(comment.hearted)
        XCTAssertNotNil(comment.published)
        XCTAssertEqual(comment.replies.count, 1)
        XCTAssertTrue(comment.replies[0].fromUploader)
    }

    /// An older archive kept only the relative wording, and a server that
    /// predates a field sends nothing at all — neither may fail the response.
    func testAThinCommentStillDecodes() throws {
        let comment = try decoder.decode(
            VideoComment.self,
            from: Data(#"{"id":"c1","author":"@early","text":"First.","time_text":"2 days ago"}"#.utf8)
        )
        XCTAssertNil(comment.published)
        XCTAssertEqual(comment.likes, 0)
        XCTAssertTrue(comment.replies.isEmpty)
    }

    /// The choice between the archived date and upstream's wording is the
    /// kit's, so the phone and the TV cannot make it differently.
    func testWhenPrefersTheArchivedDate() {
        let dated = VideoComment(id: "c1", author: "@a", text: "x",
                                 published: Date(timeIntervalSince1970: 0), timeText: "2 days ago")
        XCTAssertEqual(dated.when { _ in "yesterday" }, "yesterday")

        let undated = VideoComment(id: "c2", author: "@a", text: "x", timeText: "2 days ago")
        XCTAssertEqual(undated.when { _ in "yesterday" }, "2 days ago")

        let neither = VideoComment(id: "c3", author: "@a", text: "x")
        XCTAssertEqual(neither.when { _ in "yesterday" }, "")
    }

    func testTheInitialSkipsTheAtSign() {
        XCTAssertEqual(VideoComment(id: "c", author: "@someone", text: "x").initial, "S")
        XCTAssertEqual(VideoComment(id: "c", author: "Someone", text: "x").initial, "S")
        XCTAssertEqual(VideoComment(id: "c", author: "@", text: "x").initial, "?")
        XCTAssertEqual(VideoComment(id: "c", author: "", text: "x").initial, "?")
    }

    func testAPageOfCommentsDecodes() throws {
        let json = """
        {"items":[{"id":"c1","author":"@a","text":"x","likes":0,"replies":[]}],
         "page":0,"page_size":30,"total":1,"has_more":false}
        """
        let page = try decoder.decode(Page<VideoComment>.self, from: Data(json.utf8))
        XCTAssertEqual(page.items.count, 1)
        XCTAssertFalse(page.hasMore)
    }
}
