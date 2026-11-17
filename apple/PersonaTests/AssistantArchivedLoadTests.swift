import Foundation
import Testing
@testable import Persona
import PersonaWire

/// Where the sidebar's Archived and All Chats get their rows on a paged hub: the assistant settings' read
/// (`AppModel.refreshAssistant`), which has to ask for the archived conversations too (`?archived=1`). The hub leaves them
/// out otherwise (docs/assistant/assistant.md "Archive only hides"). Without it Archived stayed empty, and a chat archived
/// on another device stayed in Recent and All Chats. Made-up values: 60 open conversations and 15 archived ones.
@MainActor
struct AssistantArchivedLoadTests {
    /// This device's time zone, so reading the settings sends none of its own.
    private static let zone = TimeZone.current.identifier

    /// The hub's conversations: id → archived. The main one is `c00`.
    @MainActor private final class Book {
        var archived: [String: Bool] = [:]
        var extra: [String] = []
        init() {
            for n in 0..<60 { archived[String(format: "c%02d", n)] = false }
            for n in 0..<15 { archived[String(format: "a%02d", n)] = true }
        }
    }

    /// The hub's answer to `GET /api/v1/assistant`: archived ones only with `?archived=1` (or `true`), as the hub.
    private static func state(_ book: Book, _ req: LocalRequest) -> Data {
        let withArchived = ["1", "true"].contains(req.query["archived"] ?? "")
        let rows = book.archived.keys.sorted().enumerated().compactMap { i, id -> String? in
            let archived = book.archived[id] == true
            guard withArchived || !archived else { return nil }
            let at = ISO8601DateFormatter().string(from: Date(timeIntervalSince1970: 1_800_000_000 - Double(i) * 60))
            return #"{"machine_id":"box","session_id":"\#(id)","title":"Chat \#(id)","status":"idle","state":"idle","assistant":true,"archived":\#(archived),"updated_at":"\#(at)"}"#
        } + book.extra
        let body = #"{"machine_id":"box","launcher":"claude","cwd":"/a","main":"c00","timezone":"\#(zone)","conversations":[\#(rows.joined(separator: ","))]}"#
        return Data(body.utf8)
    }

    /// A model on one made-up hub whose session list is paged (a current hub): Persona's chats reach it only through
    /// the settings' read.
    private func model(_ hub: FakeHub, _ book: Book) async throws -> AppModel {
        hub.on("GET", "/api/v1/assistant") { req in Self.state(book, req) }
        let transport = try #require(hub.client.transport)
        let model = AppModel(stateFolder: FileManager.default.temporaryDirectory.appending(path: "assistant-archived-" + UUID().uuidString))
        model.linkHooksForTests = (transport: transport, socket: { SyncSocket() })
        model.connectOnly(.demo)
        let list = SessionListModel<Session>(id: { $0.id }, removalID: { SessionListIdentity.runtime($0, group: "")?.path },
            fetch: { _, _, _ in SessionListPage(sessions: [], remove: [], hasMore: false, nextCursor: nil, revision: "r0", total: 0) },
            counts: { _ in SessionListCounts(total: 0, machine: [:], project: [:], group: [:], state: [:], revision: "r0") })
        await list.load()
        #expect(list.paged)
        model.sessionLists.setForTests(list, group: "")
        return model
    }

    private func ids(_ rows: [Session]) -> Set<String> { Set(rows.map(\.sessionID)) }

    @Test func theSidebarListsActiveArchivedAndAllChats() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current")
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        let model = try await model(hub, Book())
        await model.refreshAssistant()

        #expect(hub.sent("GET", "/api/v1/assistant").last?.query["archived"] == "1", "the sidebar's read asks for archived ones")
        // Active (the sidebar's lists), Archived, and All Chats (every open one but the main, as `AutoConversationsView`).
        #expect(model.assistantConversations.count == 60)
        #expect(model.assistantConversations.allSatisfy { $0.archived != true })
        #expect(model.archivedConversations.count == 15, "Archived lists what the hub archived")
        #expect(ids(model.archivedConversations) == Set((0..<15).map { String(format: "a%02d", $0) }))
        let allChats = model.assistantConversations.filter { $0.key != model.assistantMain }
        #expect(allChats.count == 59)
        #expect(ids(allChats).isDisjoint(with: ids(model.archivedConversations)))
    }

    /// Archived on the web or by the assistant: the next read moves it out of Recent and All Chats into Archived. The
    /// landing keeps rows it does not see again (`landSessionSummaries`), so it must be told, not left out.
    @Test func aChatArchivedElsewhereMovesToArchived() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current")
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        let book = Book()
        let model = try await model(hub, book)
        await model.refreshAssistant()
        #expect(ids(model.assistantConversations).contains("c05"))

        book.archived["c05"] = true
        await model.refreshAssistant()
        #expect(!ids(model.assistantConversations).contains("c05"), "still listed as open")
        #expect(ids(model.archivedConversations).contains("c05"))
        #expect(model.assistantConversations.count == 59 && model.archivedConversations.count == 16)
    }

    /// Unarchive in Archived, or the assistant restoring it: back in the open lists, and it stays there on the next read.
    @Test func aRestoredChatComesBack() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current")
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        let book = Book()
        hub.on("POST", "/api/v1/assistant/conversations/a03/unarchive") { _ in
            book.archived["a03"] = false
            return Data(#"{"ok":true}"#.utf8)
        }
        let model = try await model(hub, book)
        await model.refreshAssistant()
        #expect(ids(model.archivedConversations).contains("a03"))

        await model.archiveConversation(SessionKey(group: "", machineID: "box", sessionID: "a03"), false)
        #expect(ids(model.assistantConversations).contains("a03"))
        #expect(!ids(model.archivedConversations).contains("a03"))
        await model.refreshAssistant()
        #expect(ids(model.assistantConversations).contains("a03") && model.archivedConversations.count == 14)

        // Restored by the assistant (its tool, not this phone): the next read brings it back too.
        book.archived["a04"] = false
        await model.refreshAssistant()
        #expect(ids(model.assistantConversations).contains("a04") && !ids(model.archivedConversations).contains("a04"))
    }

    /// One row that does not decode (an odd `archived`, no id) is left out; the other conversations, and rows whose
    /// `archived` is null or missing (open), still list.
    @Test func oneBadRowDoesNotEmptyTheLists() async throws {
        let chosen = UserDefaults.standard.object(forKey: "assistant.current")
        defer { UserDefaults.standard.set(chosen, forKey: "assistant.current") }
        let hub = FakeHub()
        let book = Book()
        book.extra = [
            #"{"machine_id":"box","session_id":"odd","assistant":true,"archived":"yes"}"#,
            #"{"machine_id":"box","assistant":true,"archived":true}"#,
            #"{"machine_id":"box","session_id":"null-archived","assistant":true,"archived":null,"title":null}"#,
            #"{"machine_id":"box","session_id":"no-archived","assistant":true}"#,
        ]
        let model = try await model(hub, book)
        await model.refreshAssistant()
        #expect(model.assistantConfig != nil, "one bad row failed the whole read")
        #expect(model.assistantConversations.count == 62)
        #expect(ids(model.assistantConversations).isSuperset(of: ["null-archived", "no-archived"]))
        #expect(!ids(model.assistantConversations).contains("odd") && !ids(model.archivedConversations).contains("odd"))
        #expect(model.archivedConversations.count == 15)
    }

    /// Only the rows are read one by one: an answer without its list, or with something else in its place, still fails.
    @Test func aMissingOrBrokenListStillFails() {
        let decode = { (json: String) in try JSON.decoder.decode(APIClient.AssistantState.self, from: Data(json.utf8)) }
        #expect(throws: (any Error).self) { try decode(#"{"machine_id":"box","launcher":"claude","cwd":"/a"}"#) }
        #expect(throws: (any Error).self) { try decode(#"{"machine_id":"box","launcher":"claude","cwd":"/a","conversations":{}}"#) }
        let st = try? decode(#"{"machine_id":"box","launcher":"claude","cwd":"/a","conversations":[{"session_id":"x"},{"session_id":7}]}"#)
        #expect(st?.conversations.map(\.sessionID) == ["x"])
    }
}
