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
        app.launchArguments = ["-pairURL", pair]
        app.launchPinned()
        app.terminate()
        app.launchArguments = ["-pairURL", pair, "-tab", "dev", "-app.tab", "chats", "-ui.style", "glass", "-ui.scheme", style,
                               "-openSession", "macbook/c0de0000-0000-4000-8000-000000014527", "-AppleLanguages", "(en)", "-AppleLocale", "en_US"]
        if largest { app.launchArguments += ["-UIPreferredContentSizeCategoryName", "UICTContentSizeCategoryAccessibilityXXXL"] }
        app.launchPinned()
        let field = unmasked ? app.textFields["interaction.field.input"] : app.secureTextFields["interaction.field.input"]
        let send = app.buttons["interaction.commandInput.send"]
        let decline = app.buttons["interaction.commandInput.defer"]
        XCTAssertTrue(field.waitForExistence(timeout: 30), app.debugDescription)
        XCTAssertTrue(send.exists)
        XCTAssertFalse(send.isEnabled)
        if mode == "send", let directory = env["PERSONA_SHOTS"] {
            let size = largest ? "largest" : "standard"
            try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: directory)
                .appending(path: "command-input-\(style)-\(size).png"))
        }

        if !field.isHittable { app.scrollViews["pending-cards"].firstMatch.swipeUp() }
        field.tap()
        if !unmasked && !largest && mode == "send" {
            let reveal = app.buttons["interaction.field.input-reveal"]
            XCTAssertTrue(reveal.waitForExistence(timeout: 5))
            reveal.tap()
            XCTAssertTrue(app.textFields["interaction.field.input"].exists)
            reveal.tap()
            XCTAssertTrue(field.exists)
        }
        field.typeText("example-line")
        XCTAssertTrue(send.isEnabled)
        let returnKey = app.keyboards.buttons.matching(NSPredicate(format: "label IN %@", ["Return", "return", "Done"])).firstMatch
        if returnKey.exists && returnKey.isHittable { returnKey.tap() }

        func receipts() -> [[String: Any]] {
            guard let data = try? Data(contentsOf: URL(string: hub + "/mock/resolved")!),
                  let response = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
                  let rows = response["resolved"] as? [[String: Any]] else { return [] }
            return rows.filter { $0["id"] as? String == "ci_0c14527a5e1f2b3c" }
        }
        XCTAssertTrue(receipts().isEmpty, "The input field did not send a reply")

        let button = mode == "decline" ? decline : send
        if !button.isHittable { app.scrollViews["pending-cards"].firstMatch.swipeUp() }
        button.tap()
        if mode == "refuse" {
            XCTAssertTrue(app.staticTexts["interaction.commandInput.refusal"].waitForExistence(timeout: 15))
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
