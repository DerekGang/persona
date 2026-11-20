import Foundation
import PersonaWire
import Testing
@testable import Persona

@MainActor struct KBProjectEvidenceTests {
    private let entry = Data(#"""
    {"kind":"projects","id":"sample","name":"Sample delivery","relevance":5,"updated":"2026-11-19T15:08:12Z",
     "project_knowledge":{"policy":"managed","position":3,"needs_review":true,"fields":{
      "summary":{"status":"needs_review","cutoff":1,"uncovered":2,"locked":true,"maintainer":"user"},
      "one_line":{"status":"current","cutoff":3,"writer":"pipeline","maintainer":"pipeline","source_refs":["chats/sample"]}}}}
    """#.utf8)

    @Test func projectRowsRetainBothReceiptsAtTheirOwnCutoffs() throws {
        let row = try JSON.decoder.decode(KBEntry.self, from: entry)
        let encoded = try JSONSerialization.jsonObject(with: JSONEncoder().encode(row)) as? [String: Any]
        let knowledge = try #require(encoded?["project_knowledge"] as? [String: Any])
        let fields = try #require(knowledge["fields"] as? [String: [String: Any]])
        #expect(fields["summary"]?["cutoff"] as? Int == 1)
        #expect(fields["summary"]?["uncovered"] as? Int == 2)
        #expect(fields["summary"]?["locked"] as? Bool == true)
        #expect(fields["one_line"]?["cutoff"] as? Int == 3)
    }
}

extension KBProjectEvidenceTests {
    private func project(_ policy: String = "managed", explicit: [String] = ["docs/manual"]) throws -> KBDetail {
        let obj: [String: Any] = [
            "kind": "projects", "id": "same-slug", "path": "projects/same-slug", "meta": ["source_refs": explicit],
            "project_knowledge": ["policy": policy, "position": 3, "needs_review": true,
                                  "fields": ["summary": ["status": "needs_review", "uncovered": 3, "locked": true],
                                             "one_line": ["status": "current", "cutoff": 0]],
                                  "sources": [["ref": "docs/manual"], ["ref": "chats/change", "state": "merged",
                                                                      "state_at": "2026-11-19T15:08:12.123456789Z"]]]
        ]
        return try JSON.decoder.decode(KBDetail.self, from: JSONSerialization.data(withJSONObject: obj))
    }

    private func body(_ draft: KBProjectStorageDraft) throws -> [String: Any] {
        let update = try #require(draft.update)
        return try #require(JSONSerialization.jsonObject(with: JSONEncoder().encode(update)) as? [String: Any])
    }

    @Test func oldHubAndOtherKindsNeverProduceAStorageEditor() throws {
        let noPolicy = Data(#"{"kind":"projects","id":"old","path":"projects/old","meta":{"source_refs":["docs/manual"]}}"#.utf8)
        let old = try JSON.decoder.decode(KBDetail.self, from: noPolicy)
        #expect(old.projectKnowledge == nil && KBProjectStorageDraft(old) == nil)
        var nonProject = try project()
        nonProject.kind = "chats"
        #expect(KBProjectStorageDraft(nonProject) == nil)
        #expect(try KBProjectStorageDraft(project("future_rule")) == nil)
    }

    @Test func historicalReceiptsKeepUnknownDistinctFromZeroAndSourceStateIndependent() throws {
        let detail = try project()
        let evidence = try #require(detail.projectKnowledge)
        let summary = try #require(evidence.fields["summary"])
        #expect(summary.cutoff == nil && summary.writer == nil && summary.maintainer == nil)
        #expect(summary.status == "needs_review" && summary.uncovered == 3 && summary.locked == true)
        #expect(evidence.fields["one_line"]?.cutoff == 0)
        let source = try #require(evidence.sources?.last)
        #expect(source.ref == "chats/change" && source.state == "merged")
        #expect(source.stateAt == JSON.parseDate("2026-11-19T15:08:12.123456789Z"))
    }

    @Test func loadedDetailReplacesTheListsOlderReceiptEvenWhenNewerHubInfoIsAbsent() throws {
        let row = try JSON.decoder.decode(KBEntry.self, from: entry)
        let updated = try project("reference_only")
        #expect(KnowledgeDetailView.shown(row, detail: .init(detail: updated)).projectKnowledge?.policy == "reference_only")
        var old = updated
        old.projectKnowledge = nil
        #expect(KnowledgeDetailView.shown(row, detail: .init(detail: old)).projectKnowledge == nil)
        #expect(KnowledgeDetailView.shown(row, detail: nil).projectKnowledge == row.projectKnowledge)
    }

    @Test func storageWritesOnlyExplicitlyChangedFieldsAndCanClearOnlyTheOwnersPointers() throws {
        var draft = try #require(KBProjectStorageDraft(project()))
        #expect(draft.references == "docs/manual" && draft.update == nil)
        draft.references = "  docs/manual \n\n"
        #expect(draft.update == nil)
        draft.policy = .referenceOnly
        let onlyPolicy = try body(draft)
        #expect(Set(onlyPolicy.keys) == ["storage_policy"] && onlyPolicy["storage_policy"] as? String == "reference_only")
        draft.references = "\n"
        let changed = try body(draft)
        #expect(Set(changed.keys) == ["storage_policy", "source_refs"] && changed["source_refs"] as? [String] == [])
        draft.policy = .managed
        #expect(Set(try body(draft).keys) == ["source_refs"])
        draft.references = "chats/public\nhttps://example.org/release"
        #expect(try body(draft)["source_refs"] as? [String] == ["chats/public", "https://example.org/release"])
        var discoveredOnly = try #require(KBProjectStorageDraft(project(explicit: [])))
        #expect(discoveredOnly.references.isEmpty && discoveredOnly.update == nil)
        discoveredOnly.policy = .referenceOnly
        #expect(try body(discoveredOnly)["source_refs"] == nil)
        var back = try #require(KBProjectStorageDraft(project("reference_only")))
        back.policy = .managed
        #expect(try body(back)["storage_policy"] as? String == "managed")
    }

    @Test func sameSlugHasOneOwnerAcrossReadsSavesManagerAndCredentialChanges() async throws {
        let hub = FakeHub()
        let path = "/api/v1/kb/projects/same-slug"
        hub.on("GET", path, json: #"{"detail":{"kind":"projects","id":"same-slug","path":"projects/same-slug","meta":{},"project_knowledge":{"policy":"managed","position":0,"needs_review":false,"fields":{}}}}"#)
        hub.on("PUT", path, json: "{}")
        for name: String? in [nil, "work"] {
            let current = hub.client.showing(name)
            let opened = current.currentAssistantSettings()
            let result = try await opened.kbDetail("projects", id: "same-slug")
            var draft = try #require(KBProjectStorageDraft(result.detail))
            try await opened.saveProjectStorage(draft, id: "same-slug", current: current)
            draft.policy = .referenceOnly
            var changedCredential = current
            changedCredential.config.token = "other-fixture"
            for other in [hub.client.showing(name == nil ? "work" : nil), FakeHub(url: "https://second.invalid").client.showing(name),
                          changedCredential, nil] {
                await #expect(throws: CancellationError.self) {
                    try await opened.saveProjectStorage(draft, id: "same-slug", current: other)
                }
            }
            try await opened.saveProjectStorage(draft, id: "same-slug", current: current)
        }
        #expect(hub.sent("GET", path).map { $0.headers[APIClient.managerHeader] } == [nil, "work"])
        #expect(hub.sent("PUT", path).map { $0.headers[APIClient.managerHeader] } == [nil, "work"])
    }
}
