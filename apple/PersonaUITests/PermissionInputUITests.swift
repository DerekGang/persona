import XCTest

final class PermissionInputUITests: XCTestCase {
    @MainActor func testCommandInputUsesOneExplicitReply() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_MOCK_HUB"], let mode = env["PERSONA_COMMAND_INPUT_MODE"] else {
            throw XCTSkip("needs the MOCK_COMMAND_INPUT fixture from #14527")
        }
        let style = env["PERSONA_STYLE"] ?? "light"
        let largest = env["PERSONA_COMMAND_INPUT_LARGE"] == "1"
        let unmasked = mode == "decline"
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-app.tab", "chats", "-ui.style", "glass", "-ui.scheme", style,
                               "-dev.openChat", "", "-openAgentsFresh", "YES", "-openChatsFresh", "YES",
                               "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        if largest { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launchPinned()
        let field = unmasked ? app.textFields["interaction.field.input"] : app.secureTextFields["interaction.field.input"]
        let send = app.buttons["interaction.commandInput.send"]
        let decline = app.buttons["interaction.commandInput.defer"]
        let form = app.scrollViews["interaction.form-scroll"].firstMatch
        let cards = app.scrollViews["pending-cards"].firstMatch
        func reveal(_ control: XCUIElement, down: Bool = false) {
            for _ in 0..<5 where !control.isHittable {
                let scroll = form.exists && form.isHittable ? form : (cards.exists ? cards : app)
                if down { scroll.swipeDown(velocity: .slow) }
                else { scroll.swipeUp(velocity: .slow) }
            }
            XCTAssertTrue(control.isHittable, app.debugDescription)
        }

        let needsYou = app.buttons["agents.filter.needsYou"]
        XCTAssertTrue(needsYou.waitForExistence(timeout: 20))
        needsYou.tap()
        let preview = app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "Sign in to the cloud CLI")).firstMatch
        for _ in 0..<4 where !preview.isHittable { app.swipeUp(velocity: .slow) }
        if preview.isHittable {
            preview.tap()
        } else {
            // The transition is also reachable from the chat link once the app has connected.
            app.open(URL(string: "persona://open?machine=macbook&session=c0de0000-0000-4000-8000-000000014527")!)
        }
        if largest, !field.exists, form.exists {
            for _ in 0..<3 where !field.exists { form.swipeUp(velocity: .slow) }
        }
        guard field.waitForExistence(timeout: 20), send.exists else {
            XCTFail(app.debugDescription)
            return
        }
        XCTAssertTrue(app.staticTexts["Waiting for your input"].exists)
        XCTAssertFalse(send.isEnabled)
        if mode == "send", let directory = env["PERSONA_SHOTS"] {
            let size = largest ? "largest" : "standard"
            try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: directory)
                .appending(path: "command-input-\(style)-\(size).png"))
        }

        reveal(field)
        field.tap()
        field.typeText("example-line")
        if !unmasked && !largest && mode == "send" {
            let toggle = app.buttons["interaction.field.input-reveal"]
            XCTAssertTrue(toggle.waitForExistence(timeout: 5))
            toggle.tap()
            XCTAssertEqual(app.textFields["interaction.field.input"].value as? String, "example-line")
            toggle.tap()
            XCTAssertTrue(field.exists)
        }
        XCTAssertTrue(send.isEnabled)
        field.typeText("\n")

        func receipts() -> [[String: Any]] {
            guard let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
                  let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let rows = response["resolved"] as? [[String: Any]] else { return [] }
            return rows.filter { $0["id"] as? String == "ci_0c14527a5e1f2b3c" }
        }
        XCTAssertTrue(receipts().isEmpty, "The input field did not send a reply")

        if mode == "fold" {
            let fold = app.buttons["interaction-fold"].firstMatch
            reveal(fold, down: true)
            fold.tap()
            XCTAssertTrue(field.waitForNonExistence(timeout: 10))
            fold.tap()
            XCTAssertTrue(field.waitForExistence(timeout: 10))
            XCTAssertFalse(send.isEnabled)
            XCTAssertTrue(receipts().isEmpty)
            return
        }

        let button = mode == "decline" ? decline : send
        reveal(button)
        button.tap()
        if mode == "refuse" {
            let refusal = app.staticTexts["interaction.commandInput.refusal"]
            XCTAssertTrue(refusal.waitForExistence(timeout: 15))
            XCTAssertEqual(refusal.label, "The test command refused the line.")
            XCTAssertFalse(send.isEnabled, "Re-enter the line after a refusal")
            XCTAssertTrue(field.exists)
            return
        }
        let receipt = expectation(for: NSPredicate { _, _ in !receipts().isEmpty }, evaluatedWith: nil)
        XCTAssertEqual(XCTWaiter.wait(for: [receipt], timeout: 10), .completed)
        let received = receipts()
        XCTAssertEqual(received.count, 1)
        let resolution = received.first?["resolution"] as? [String: Any]
        XCTAssertEqual(resolution?["allow"] as? Bool, mode == "send")
        let values = resolution?["values"] as? [String: String]
        XCTAssertEqual(values?["input"], mode == "send" ? "example-line" : nil)
        XCTAssertTrue(field.waitForNonExistence(timeout: 10))
        PasswordPrompt.assertNone(app, "a command input reply")
    }

    @MainActor func testSecureInputIsRequiredBeforeApproval() throws {
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs MOCK_SETTINGS_INPUT=1 scripts/mock/mock_hub.py")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-app.tab", "chats", "-openSession", "macbook/a1f3c2d4-0000-4000-8000-000000000006", "-ui.style", "glass"]
        app.launch()
        let field = app.secureTextFields["interaction.field.password"]
        XCTAssertTrue(field.waitForExistence(timeout: 20), app.debugDescription)
        let allow = app.buttons["填写并允许"]
        XCTAssertFalse(allow.isEnabled)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "permission-input.png"))
        }
        field.tap()
        field.typeText("example-password")
        XCTAssertTrue(allow.isEnabled)
        allow.tap()
        let deadline = Date().addingTimeInterval(5)
        while Date() < deadline {
            if let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
               let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
               let rows = response["resolved"] as? [[String: Any]],
               let row = rows.first(where: { $0["id"] as? String == "ix_settings_input" }) {
                let resolution = row["resolution"] as? [String: Any]
                XCTAssertEqual(resolution?["allow"] as? Bool, true)
                XCTAssertEqual((resolution?["values"] as? [String: String])?["password"], "example-password")
                // The card goes once answered: iOS must not offer to save the value as a password (#1598).
                PasswordPrompt.assertNone(app, "a permission form's secret field")
                return
            }
            Thread.sleep(forTimeInterval: 0.1)
        }
        XCTFail("mock did not receive the form approval")
    }
}
