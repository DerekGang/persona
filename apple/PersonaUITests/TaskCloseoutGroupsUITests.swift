import XCTest

/// Run with MOCK_TASKS=1 MOCK_TASKS_NO_ASKS=1 MOCK_TASK_CLOSEOUT_GROUPS=1.
final class TaskCloseoutGroupsUITests: XCTestCase {
    @MainActor func testCloseoutsHaveSeparateGroupsAndLongWaitsStayInsideThePhone() throws {
        let env = ProcessInfo.processInfo.environment
        guard let hub = env["PERSONA_MOCK_HUB"], env["PERSONA_CLOSEOUT_GROUPS"] == "1" else {
            throw XCTSkip("Needs the deterministic five-group task mock")
        }
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        for (tray, count) in [("review", 1), ("stopped", 2), ("parked", 1)] {
            app.launchArguments = ["-pairURL", pair, "-tab", "assistant", "-dev.openChat", "",
                                   "-openAssistant", "server/b2e4d6f8-0000-4000-8000-000000000001",
                                   "-showActionTray", tray, "-AppleLanguages", "(zh-Hans)", "-AppleLocale", "zh_CN"]
            app.launchPinned()
            let rows = app.buttons.matching(identifier: "action-tray.task")
            XCTAssertTrue(rows.firstMatch.waitForExistence(timeout: 30), app.debugDescription)
            XCTAssertEqual(rows.count, count, app.debugDescription)
            for row in rows.allElementsBoundByIndex {
                XCTAssertGreaterThanOrEqual(row.frame.minX, app.frame.minX - 1, row.label)
                XCTAssertLessThanOrEqual(row.frame.maxX, app.frame.maxX + 1, row.label)
                XCTAssertFalse(row.label.contains("完成未看"), row.label)
                XCTAssertTrue(row.label.contains("待办"), row.label)
            }
            if tray == "stopped" {
                XCTAssertTrue(rows.allElementsBoundByIndex.contains { $0.label.contains("the maintainer") })
            }
            XCTAssertTrue(app.buttons["action-bar.\(tray)"].isHittable, app.debugDescription)
            if let directory = env["PERSONA_SHOTS"] {
                let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "light"
                try app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: "\(directory)/iphone-\(appearance)-\(tray).png"))
            }
            app.terminate()
        }
    }
}
