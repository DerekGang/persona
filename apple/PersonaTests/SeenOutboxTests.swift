import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Mark All as Read over a socket that is gone (#1588): a mark waits until a list from the hub shows it recorded, the
/// session stays seen meanwhile, and the mark goes again over HTTP or the next socket.
@MainActor struct SeenOutboxTests {
    private let machine = MachineRef(group: "", id: "m")
    private var key: SessionKey { SessionKey(machineID: "m", sessionID: "s1") }
    private let t0 = Date(timeIntervalSince1970: 1_000_000)

    /// s1 as the hub lists it: `seen` and `updated` are seconds after 1970.
    private func session(_ id: String = "s1", state: String, seen: TimeInterval = 0, updated: TimeInterval = 100) -> Session {
        let f = ISO8601DateFormatter()
        let json = """
        {"machine_id":"m","session_id":"\(id)","status":"done","state":"\(state)",
         "seen_at":"\(f.string(from: Date(timeIntervalSince1970: seen)))",
         "updated_at":"\(f.string(from: Date(timeIntervalSince1970: updated)))"}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    private func marked() -> SeenOutbox {
        var o = SeenOutbox()
        o.mark(key, session(state: "unread"), at: t0)
        o.sent(key, .socket, at: t0)
        return o
    }

    private func state(_ lists: [MachineRef: [Session]], _ id: String = "s1") -> AgentState? {
        lists[machine]?.first { $0.sessionID == id }?.state
    }

    @Test func aMarkTheHubHasNotTakenStaysSeen() {
        var o = marked()
        // The hub never got it: its next list still says unread, from the same turn.
        let out = o.land([machine: [session(state: "unread"), session("s2", state: "unread")]], at: t0 + 1)
        #expect(state(out) == .idle)
        #expect(state(out, "s2") == .unread) // not marked here
        #expect(o.marks[key] != nil)
        // The machine touching the transcript within the same turn is not a new one.
        #expect(state(o.land([machine: [session(state: "unread", updated: 101)]], at: t0 + 2)) == .idle)
    }

    @Test func theHubRecordingItLetsItGo() {
        var o = marked()
        let out = o.land([machine: [session(state: "idle", seen: 500)]], at: t0 + 1)
        #expect(state(out) == .idle)
        #expect(o.marks.isEmpty)
    }

    @Test func aTurnFinishedAfterTheMarkIsUnread() {
        var o = marked()
        let out = o.land([machine: [session(state: "unread", updated: 150)]], at: t0 + 1)
        #expect(state(out) == .unread)
        #expect(o.marks.isEmpty)
    }

    @Test func anAgentThatMovedOnLetsItGo() {
        var working = marked()
        #expect(state(working.land([machine: [session(state: "working")]], at: t0 + 1)) == .working)
        #expect(working.marks.isEmpty)
        var asking = marked()
        #expect(state(asking.land([machine: [session(state: "needs_you")]], at: t0 + 1)) == .needsYou)
        #expect(asking.marks.isEmpty)
        var gone = marked()
        _ = gone.land([machine: [session("s2", state: "unread")]], at: t0 + 1)
        #expect(gone.marks.isEmpty)
    }

    @Test func otherMachinesLeaveItAlone() {
        var o = marked()
        let other = MachineRef(group: "", id: "other")
        _ = o.land([other: []], at: t0 + 1)
        #expect(o.marks[key] != nil)
    }

    @Test func aStaleJustFinishedFrameStaysSeenUntilAcknowledgedOrANewTurn() throws {
        var row = session(state: "idle")
        row.work = try JSON.decoder.decode(WorkState.self, from: Data(#"{"phase":"done","line":"Merged","just_finished":true}"#.utf8))
        var outbox = SeenOutbox()
        outbox.mark(key, row, at: t0)
        let stale = try #require(outbox.land([machine: [row]], at: t0 + 1)[machine]?.first)
        #expect(stale.state == .idle && !stale.needsSeen() && stale.work?.line == "Merged")
        #expect(outbox.due(at: t0 + SeenOutbox.confirmWithin) == [key])
        var newTurn = row
        newTurn.updatedAt = row.updatedAt?.addingTimeInterval(10)
        let later = try #require(outbox.land([machine: [newTurn]], at: t0 + 10)[machine]?.first)
        #expect(later.needsSeen() && outbox.marks.isEmpty)
        outbox.mark(key, row, at: t0)
        row.seenAt = row.seenAt?.addingTimeInterval(10)
        row.markLocallySeen()
        _ = outbox.land([machine: [row]], at: t0 + 1)
        #expect(outbox.marks.isEmpty)
    }

    @Test func openingAndMarkingAllClearJustFinishedBeforeTheHubAnswers() throws {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let model = AppModel(stateFolder: folder)
        var first = session(state: "idle"), second = session("s2", state: "idle")
        first.work = try JSON.decoder.decode(WorkState.self, from: Data(#"{"phase":"done","just_finished":true}"#.utf8))
        second.work = first.work
        let unread = session("s3", state: "unread")
        model.applySessions([machine: [first, second, unread]])
        model.visibleSession = key
        model.markSeen(key)
        #expect(model.session(key)?.needsSeen() == false)
        #expect(model.heldInList == key && model.heldJustFinished)
        model.markSeen([second.key, unread.key])
        model.applySessions([machine: [first, second, unread]])
        #expect([first.key, second.key, unread.key].allSatisfy { model.session($0)?.needsSeen() == false })
        #expect(model.heldInList == key && model.heldJustFinished)
        model.visibleSession = nil
        #expect(model.heldInList == nil && !model.heldJustFinished)
    }

    @Test func onlyUnseenWorkWaits() {
        var o = SeenOutbox()
        o.mark(key, session(state: "idle"), at: t0)
        #expect(o.marks.isEmpty)
    }

    @Test func overTheSocketItGoesAgainOverHTTP() {
        var o = marked()
        #expect(o.due(at: t0 + SeenOutbox.confirmWithin - 1).isEmpty)
        #expect(o.nextDue(at: t0) == t0 + SeenOutbox.confirmWithin)
        #expect(o.due(at: t0 + SeenOutbox.confirmWithin) == [key])
        // Over HTTP: not again on a timer; once answered, only the list is waited for.
        o.sent(key, .http, at: t0 + SeenOutbox.confirmWithin)
        #expect(o.due(at: t0 + 60).isEmpty)
        #expect(o.nextDue(at: t0 + 60) == nil)
        #expect(o.unanswered(group: "", at: t0 + 60) == [key]) // the request failed: the next socket sends it
        o.answered(key)
        #expect(o.unanswered(group: "", at: t0 + 60).isEmpty)
        #expect(state(o.land([machine: [session(state: "unread")]], at: t0 + 61)) == .idle) // its list is not here yet
    }

    @Test func eachGroupSendsItsOwn() {
        var o = marked()
        #expect(o.unanswered(group: "work", at: t0).isEmpty)
        #expect(o.unanswered(group: "", at: t0) == [key])
    }

    @Test func itGivesUpAfterAWhile() {
        var o = marked()
        let late = t0 + SeenOutbox.keepFor + 1
        #expect(o.due(at: late).isEmpty)
        #expect(o.unanswered(group: "", at: late).isEmpty)
        #expect(state(o.land([machine: [session(state: "unread")]], at: late)) == .unread)
        #expect(o.marks.isEmpty)
    }
}
