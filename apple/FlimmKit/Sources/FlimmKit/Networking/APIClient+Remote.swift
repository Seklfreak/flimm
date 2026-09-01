import Foundation

// Remote control: a player publishes what it is doing, anything else signed in
// as the same account reads it and sends commands back.
//
// Two of these calls are long polls — they may take the better part of half a
// minute to answer, and that is the normal case rather than a hang. Neither
// belongs on a code path that blocks a screen.
extension APIClient {

    /// Publishes what this player is doing. Upsert: the same session id is put
    /// again for every heartbeat and every change.
    ///
    /// The server stamps the time and owns the id, so ``RemoteSession/updatedAt``
    /// on the way out is ignored. Use ``RemotePublisher`` rather than calling
    /// this on a timer of your own — when to publish is a rule
    /// (``RemotePublishRule``), and it is only written once.
    public func publishRemoteSession(_ id: String, _ session: RemoteSession) async throws {
        try await discard(.put, "/playback/sessions/\(esc(id))", body: session)
    }

    /// Retires the session because playback stopped, rather than leaving a
    /// controller to wait out the expiry. Ending one that is already gone
    /// succeeds, so tearing down twice is safe.
    public func endRemoteSession(_ id: String) async throws {
        try await discard(.delete, "/playback/sessions/\(esc(id))")
    }

    /// Every screen of this account's that is playing.
    ///
    /// With `since` — the version from the previous answer — this is a long
    /// poll: it returns the moment anything changes, and after the server's own
    /// wait otherwise. Without one it answers at once, which is what a
    /// controller that has just opened wants.
    public func remoteSessions(since: UInt64? = nil) async throws -> RemoteSessions {
        var query = QueryBuilder()
        query.add("since", since.map(String.init))
        return try await get("/playback/sessions", query: query.items)
    }

    /// What has been sent to this player since `after`. A long poll: it holds
    /// until somebody presses something.
    ///
    /// Adopt ``RemoteCommandBatch/cursor`` whether or not commands came with
    /// it; see that type.
    public func remoteCommands(_ id: String, after: UInt64) async throws -> RemoteCommandBatch {
        var query = QueryBuilder()
        query.add("after", String(after))
        return try await get("/playback/sessions/\(esc(id))/commands", query: query.items)
    }

    /// Sends one instruction to a player. The returned sequence number is the
    /// order it will be applied in; nothing acknowledges that it was.
    @discardableResult
    public func sendRemoteCommand(_ id: String, _ command: RemoteCommand) async throws -> UInt64 {
        struct Accepted: Decodable { let seq: UInt64 }
        let accepted: Accepted = try await send(.post, "/playback/sessions/\(esc(id))/commands", body: command)
        return accepted.seq
    }
}
