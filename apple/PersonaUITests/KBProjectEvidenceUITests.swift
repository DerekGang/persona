import XCTest

final class KBProjectEvidenceUITests: XCTestCase {
    private var hub: URL!

    override func setUp() async throws {
        continueAfterFailure = false
        guard let address = ProcessInfo.processInfo.environment["PERSONA_PROJECT_HUB"], let url = URL(string: address) else {
            throw XCTSkip("Needs the project evidence mock Hub")
        }
        hub = url
        let (bytes, _) = try await URLSession.shared.data(from: hub.appending(path: "mock/project-evidence/reset"))
        guard (try? JSONDecoder().decode([String: String].self, from: bytes))?["fixture"] == "sample-delivery" else {
            throw XCTSkip("Needs the sample-delivery project fixture")
        }
    }

    @MainActor private func launch(_ appearance: String = "light", largest: Bool = false) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.absoluteString.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-AppleLanguages", "(en)", "-AppleLocale", "en_US", "-pairURL", pair,
                               "-tab", "assistant", "-ui.scheme", appearance]
        if largest { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launchPinned(navigation: false)
        let alert = XCUIApplication(bundleIdentifier: "com.apple.springboard").alerts.firstMatch
        if alert.waitForExistence(timeout: 3) {
            let okay = alert.buttons.firstMatch
            if okay.exists { okay.tap(); _ = alert.waitForNonExistence(timeout: 5) }
        }
        let place = app.buttons["sidebar-Knowledge"].firstMatch
        if !place.isHittable {
            let drawer = app.buttons["assistant-sidebar-button"].firstMatch
            XCTAssertTrue(drawer.waitForExistence(timeout: 15), app.debugDescription)
            drawer.tap()
        }
        XCTAssertTrue(place.waitForExistence(timeout: 10), app.debugDescription)
        place.tap()
        return app
    }

    @MainActor private func row(_ app: XCUIApplication, named name: String = "Sample delivery") -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label CONTAINS %@", name)).firstMatch
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let a = XCTAttachment(screenshot: app.screenshot())
        a.name = "kb-project-" + name
        a.lifetime = .keepAlways
        add(a)
    }

    @MainActor func testListNamesTheFieldAndItsUncoveredChanges() {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
        shot(app, "list")
        XCTAssertTrue(project.label.contains("Summary needs review"), project.label)
        XCTAssertTrue(project.label.contains("2 newer"), project.label)
        XCTAssertFalse(project.label.contains("Overview needs review"), project.label)
    }

    @MainActor func testDetailExposesCoverageOnlyWhenOpened() {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        XCTAssertTrue(app.staticTexts["Sample delivery"].waitForExistence(timeout: 12), app.debugDescription)
        shot(app, "detail")
        let sources = app.buttons["kb.project.evidence"]
        XCTAssertTrue(sources.waitForExistence(timeout: 4), app.debugDescription)
        XCTAssertFalse(label(app, containing: "Writer unknown").exists)
        sources.tap()
        let historic = label(app, containing: "Writer unknown")
        XCTAssertTrue(historic.exists, app.debugDescription)
        XCTAssertTrue(historic.label.contains("Locked") && historic.label.contains("Evidence covered: 1 of 3"))
        XCTAssertTrue(historic.label.contains("Maintained by you"))
        reveal(app, app.buttons["kb.project.edit"])
        let linked = app.buttons.matching(NSPredicate(format: "label CONTAINS %@ AND label CONTAINS %@", "Release review", "chats/sample-review")).firstMatch
        reveal(app, linked)
        XCTAssertTrue(label(app, containing: "Source status: Merged").exists, app.debugDescription)
        XCTAssertTrue(label(app, containing: "Deployment and acceptance need their own evidence").exists)
        linked.tap()
        XCTAssertTrue(app.staticTexts["Release review"].waitForExistence(timeout: 12), app.debugDescription)
        XCTAssertTrue(label(app, containing: "Source delivery history.").waitForExistence(timeout: 8), app.debugDescription)
    }
}

extension KBProjectEvidenceUITests {
    @MainActor private func label(_ app: XCUIApplication, containing text: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", text)).firstMatch
    }

    @MainActor private func reveal(_ app: XCUIApplication, _ element: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        let list = app.collectionViews.firstMatch
        for _ in 0..<8 where !element.isHittable { list.swipeUp(velocity: .slow) }
        XCTAssertTrue(element.isHittable, app.debugDescription, file: file, line: line)
    }

    @MainActor private func editor(_ app: XCUIApplication) -> XCUIElement {
        let edit = app.buttons["kb.project.edit"]
        reveal(app, edit)
        edit.tap()
        let save = app.buttons["kb.project.save"].firstMatch
        XCTAssertTrue(save.waitForExistence(timeout: 8))
        return save
    }

    @MainActor private func changePolicy(_ app: XCUIApplication) {
        let row = app.switches["kb.project.reference-only"].firstMatch
        let control = row.switches.firstMatch
        (control.exists ? control : row).tap()
    }

    private func writes() async throws -> [[String: Any]] {
        let (data, _) = try await URLSession.shared.data(from: hub.appending(path: "mock/project-evidence/writes"))
        let result = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        return try XCTUnwrap(result["writes"] as? [[String: Any]])
    }

    @MainActor func testExplicitStorageChoicePointersAndReadOnlyHistory() async throws {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let evidence = app.buttons["kb.project.evidence"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 10), app.debugDescription)
        evidence.tap()
        var save = editor(app)
        try app.performAccessibilityAudit(for: [.sufficientElementDescription, .trait])
        let toggle = app.switches["kb.project.reference-only"].firstMatch
        let refs = app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch
        XCTAssertFalse(save.isEnabled)
        XCTAssertEqual(refs.value as? String, "https://example.org/releases/sample")
        changePolicy(app)
        XCTAssertEqual(toggle.value as? String, "1")
        shot(app, "storage-reference-only")
        app.buttons["sheet-close"].firstMatch.tap()
        let cancelled = try await writes()
        XCTAssertTrue(cancelled.isEmpty)
        save = editor(app)
        changePolicy(app)
        XCTAssertEqual(toggle.value as? String, "1")
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8), app.debugDescription)
        let saved = try await writes()
        XCTAssertEqual(saved.count, 1)
        let first = try XCTUnwrap(saved[0]["body"] as? [String: String])
        XCTAssertEqual(first, ["storage_policy": "reference_only"])
        XCTAssertFalse(app.buttons["kb.facts.edit"].exists)
        _ = editor(app)
        XCTAssertEqual(toggle.value as? String, "1")
        for _ in 0..<3 {
            guard let value = refs.value as? String, !value.isEmpty, value != "Source references" else { break }
            refs.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.9)).tap()
            refs.typeText(String(repeating: XCUIKeyboardKey.delete.rawValue, count: value.count + 2))
        }
        XCTAssertTrue(save.isEnabled, app.debugDescription)
        save.tap()
        XCTAssertTrue(save.waitForNonExistence(timeout: 8))
        let cleared = try await writes()
        let second = try XCTUnwrap(cleared.last?["body"] as? [String: [String]])
        XCTAssertEqual(second, ["source_refs": []])
        let linked = app.buttons.matching(NSPredicate(format: "label CONTAINS %@", "chats/sample-review")).firstMatch
        reveal(app, linked)
        let facts = app.staticTexts["kb.facts"]
        reveal(app, facts)
        XCTAssertTrue(facts.label.contains("Keep the original checklist."))
        XCTAssertFalse(app.buttons["kb.facts.edit"].exists)
        app.terminate()

        let again = launch()
        let reopened = row(again)
        XCTAssertTrue(reopened.waitForExistence(timeout: 15))
        reopened.tap()
        let section = again.buttons["kb.project.evidence"]
        XCTAssertTrue(section.waitForExistence(timeout: 10))
        XCTAssertTrue(section.label.contains("References only"), section.label)
        shot(again, "detail-reference-only")
        section.tap()
        let restore = editor(again)
        changePolicy(again)
        XCTAssertTrue(restore.isEnabled)
        restore.tap()
        XCTAssertTrue(restore.waitForNonExistence(timeout: 8))
        let restored = try await writes()
        XCTAssertEqual(restored.last?["body"] as? [String: String], ["storage_policy": "managed"])
    }

    @MainActor func testRejectedPointerKeepsTheDraftInTheEditor() async throws {
        let app = launch()
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let disclosure = app.buttons["kb.project.evidence"]
        XCTAssertTrue(disclosure.waitForExistence(timeout: 10))
        disclosure.tap()
        let save = editor(app)
        let pointers = app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch
        pointers.tap()
        pointers.typeText("bad pointer")
        save.tap()
        XCTAssertTrue(app.staticTexts["kb.project.error"].waitForExistence(timeout: 8), app.debugDescription)
        XCTAssertTrue((pointers.value as? String)?.contains("bad pointer") == true)
        XCTAssertTrue(save.isEnabled)
        let attempts = try await writes()
        XCTAssertTrue(attempts.isEmpty)
    }

    @MainActor func testOlderHubRetainsOriginalProjectControls() {
        let app = launch()
        let project = row(app, named: "Archive index")
        XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
        XCTAssertFalse(project.label.contains("needs review"))
        project.tap()
        XCTAssertTrue(app.staticTexts["Archive index"].waitForExistence(timeout: 10))
        XCTAssertFalse(app.buttons["kb.project.evidence"].exists)
        reveal(app, app.buttons["kb.facts.edit"])
        XCTAssertFalse(app.buttons["kb.project.edit"].exists)
    }

    @MainActor func testLargestTypeKeepsTheProjectOverviewAndActionsInsideTheHeader() {
        let app = launch(largest: true)
        let project = row(app)
        XCTAssertTrue(project.waitForExistence(timeout: 18))
        project.tap()
        let evidence = app.buttons["kb.project.evidence"]
        XCTAssertTrue(evidence.waitForExistence(timeout: 10))
        let overview = app.staticTexts["All items delivered."].firstMatch
        XCTAssertGreaterThanOrEqual(overview.frame.minX, evidence.frame.minX - 1)
        let ask = app.buttons["Ask Persona"].firstMatch
        let start = app.buttons["Start Agent"].firstMatch
        XCTAssertTrue(ask.isHittable && start.isHittable)
        XCTAssertFalse(ask.frame.insetBy(dx: 1, dy: 1).intersects(start.frame))
        for (button, title) in [(ask, "Ask Persona"), (start, "Start Agent")] {
            XCTAssertTrue(button.staticTexts[title].firstMatch.exists, app.debugDescription)
            XCTAssertGreaterThanOrEqual(button.frame.minX, evidence.frame.minX - 1)
            XCTAssertLessThanOrEqual(button.frame.maxX, evidence.frame.maxX + 1)
        }
        shot(app, "header-fit-largest")
    }

    @MainActor func testAppearanceAndAccessibilitySizeOnProjects() {
        for appearance in ["light", "dark"] {
            for largest in [false, true] {
                let app = launch(appearance, largest: largest)
                let project = row(app)
                XCTAssertTrue(project.waitForExistence(timeout: 18), app.debugDescription)
                if !project.isHittable { reveal(app, project) }
                XCTAssertTrue(project.label.contains("Summary needs review"))
                let suffix = appearance + (largest ? "-largest" : "-default")
                shot(app, "list-" + suffix)
                project.tap()
                let evidence = app.buttons["kb.project.evidence"]
                XCTAssertTrue(evidence.waitForExistence(timeout: 10))
                reveal(app, evidence)
                evidence.tap()
                let historic = label(app, containing: "Writer unknown")
                reveal(app, historic)
                shot(app, "coverage-" + suffix)
                _ = editor(app)
                XCTAssertTrue(app.switches["kb.project.reference-only"].firstMatch.isHittable)
                XCTAssertTrue(app.descendants(matching: .any).matching(identifier: "kb.project.pointers").firstMatch.exists)
                shot(app, "storage-" + suffix)
                app.terminate()
            }
        }
    }
}
