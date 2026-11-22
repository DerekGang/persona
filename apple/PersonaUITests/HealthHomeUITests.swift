import XCTest

/// Local sample values exercise the real Health home without an intake API or access to someone's Health store.
final class HealthHomeUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    @MainActor private func launch(scheme: String = "light", largest: Bool = false,
                                   activity: String = "full", bottom: Bool = false, trends: String = "sample") throws -> XCUIApplication {
        guard let hub = env["PERSONA_HEALTH_HUB"] else { throw XCTSkip("needs PERSONA_HEALTH_HUB pointing to a local mock") }
        continueAfterFailure = false
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-openShellPage", "health", "-ui.scheme", scheme,
                               "-showHealthTodayExample", "YES", "-showHealthActivityExample", activity,
                               "-showHealthHomeTrendsExample", trends, "-showHealthBottom", bottom ? "YES" : "NO",
                               "-UIPreferredContentSizeCategoryName", largest ? "UICTContentSizeCategoryAccessibilityXXXL" : "UICTContentSizeCategoryL"]
        app.launchPinned()
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        for label in ["Allow", "允许", "OK", "好"] where springboard.buttons[label].waitForExistence(timeout: 1) {
            springboard.buttons[label].tap()
        }
        let ready = bottom ? "health-privacy" : "health-today-example"
        XCTAssertTrue(app.descendants(matching: .any)[ready].waitForExistence(timeout: 20), app.debugDescription)
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        // The first UIKit ring layout can finish after the accessibility element first appears.
        Thread.sleep(forTimeInterval: 1)
        let tree = app.debugDescription
        let capture = app.screenshot()
        let attachment = XCTAttachment(screenshot: capture)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["PERSONA_SHOTS"] {
            try capture.pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: name + ".png"))
            try tree.write(to: URL(fileURLWithPath: dir).appending(path: name + ".ax.txt"), atomically: true, encoding: .utf8)
        }
    }

    @MainActor func testHomeLightAndDarkAtDefaultAndLargestSizes() throws {
        for scheme in ["light", "dark"] { for largest in [false, true] {
            let size = largest ? "largest" : "default"
            var app = try launch(scheme: scheme, largest: largest)
            XCTAssertTrue(app.descendants(matching: .any)["health-today-example"].exists)
            XCTAssertTrue(app.otherElements["health-water-ring"].exists)
            XCTAssertTrue(app.otherElements["health-protein-ring"].exists)
            XCTAssertFalse(app.buttons["health-activity-rings"].exists)
            try shot(app, "health-top-\(scheme)-\(size)")
            app.terminate()
            app = try launch(scheme: scheme, largest: largest, bottom: true)
            try shot(app, "health-bottom-\(scheme)-\(size)")
            app.terminate()
        } }
    }

    @MainActor func testActivityPermissionUnavailableMoveAndThreeRingStates() throws {
        for state in ["allow", "unreadable", "move", "full"] {
            let app = try launch(activity: state)
            let expected = state == "allow" ? "health-activity-allow" : state == "unreadable" ? "health-activity-unreadable" : "health-activity-rings"
            let element = app.descendants(matching: .any)[expected].firstMatch
            for _ in 0..<4 where !element.isHittable { app.swipeUp() }
            XCTAssertTrue(element.waitForExistence(timeout: 10), app.debugDescription)
            if state == "move" || state == "full" {
                XCTAssertFalse(app.buttons["health-activity-rings"].exists, "the ring tile has no action")
                let value = element.value as? String ?? ""
                XCTAssertTrue(value.contains("320") && value.contains("500"), value)
                XCTAssertEqual(value.contains("25"), state == "full", "only existing goal rows are accessible")
            } else if state == "allow" {
                XCTAssertGreaterThanOrEqual(element.frame.height, 44)
            }
            try shot(app, "health-activity-\(state)")
            app.terminate()
        }
    }

    @MainActor func testEmptyTrendsLeaveNoHomeSection() throws {
        let app = try launch(trends: "empty")
        XCTAssertFalse(app.descendants(matching: .any)["health-trends"].exists)
        app.terminate()
    }

    @MainActor func testHomeLoadsTrendsBeforeTheirConditionalRowExists() throws {
        let app = try launch()
        let trends = app.descendants(matching: .any)["health-trends"]
        for _ in 0..<5 where !trends.isHittable { app.swipeUp() }
        XCTAssertTrue(trends.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertTrue(app.descendants(matching: .any)["health-trend-steps"].exists)
        try shot(app, "health-trends-home")
        app.terminate()
    }

    @MainActor func testLargestActivityKeepsTheValuesBelowTheRings() throws {
        let app = try launch(largest: true)
        let rings = app.otherElements["health-activity-rings"]
        app.swipeUp()
        XCTAssertTrue(rings.waitForExistence(timeout: 10), app.debugDescription)
        XCTAssertEqual(app.otherElements.matching(identifier: "health-activity-rings").count, 1)
        XCTAssertFalse(app.buttons["health-activity-rings"].exists)
        try shot(app, "health-activity-largest")
        app.terminate()
    }
}
