import Foundation
import Testing
@testable import Persona

@MainActor struct HiddenAgentDirsTests {
    @Test func decodingAndEditingPreserveOtherComputers() throws {
        let json = #"{"rules":[{"machine_id":"one","pattern":"~/work","enabled":true},{"machine_id":"two","pattern":"~/work","enabled":true}]}"#
        let state = try JSON.decoder.decode(HiddenAgentDirs.self, from: Data(json.utf8))
        let first = try #require(state.rules(on: "one").first)
        var disabled = first
        disabled.enabled = false
        let changed = state.replacing(first, with: disabled)
        #expect(changed.rules(on: "one").first?.enabled == false)
        #expect(changed.rules(on: "two") == state.rules(on: "two"))
        #expect(changed.replacing(disabled, with: first) == state)
        #expect(state.replacing(first, with: nil).rules == state.rules(on: "two"))
    }

    @Test func visibilityAnnotationDefaultsToVisibleAndSurvivesCoding() throws {
        let old = #"{"machine_id":"one","session_id":"s","title":"test","cwd":"/workspace","status":"idle"}"#
        var session = try JSON.decoder.decode(Session.self, from: Data(old.utf8))
        #expect(session.hiddenByDirectory == nil)
        session.hiddenByDirectory = true
        let data = try JSONEncoder().encode(session)
        #expect(try JSON.decoder.decode(Session.self, from: data).hiddenByDirectory == true)
    }

    @Test func revealingIsAnExplicitListQueryOnly() {
        var query = SessionListQuery()
        #expect(query.parameters["include_hidden"] == nil)
        query.includeHidden = true
        #expect(query.wireParameters(hierarchy: true)["include_hidden"] == "1")
        #expect(query.wireParameters(hierarchy: true)["roots"] == "1")
    }
}
