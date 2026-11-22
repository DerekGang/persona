#if DEBUG && os(iOS)
import SwiftUI
import HealthKit

/// Local examples only: no hub routes, capabilities, demo hub or real Health data are used by these switches.
enum HealthHomeSamples {
    static var showToday: Bool { UserDefaults.standard.bool(forKey: "showHealthTodayExample") }

    static func activityModel() -> HealthActivityModel {
        guard let mode = UserDefaults.standard.string(forKey: "showHealthActivityExample") else { return HealthActivityModel() }
        return HealthActivityModel(deps: .init(
            permission: { mode == "allow" ? .shouldRequest : .unnecessary },
            read: { mode == "unreadable" ? nil : reading(moveOnly: mode == "move") },
            request: {}))
    }

    static func reading(moveOnly: Bool = false) -> HealthActivityReading {
        let summary = HKActivitySummary()
        summary.activeEnergyBurned = HKQuantity(unit: .kilocalorie(), doubleValue: 320)
        summary.activeEnergyBurnedGoal = HKQuantity(unit: .kilocalorie(), doubleValue: 500)
        summary.appleExerciseTime = HKQuantity(unit: .minute(), doubleValue: 25)
        summary.appleStandHours = HKQuantity(unit: .count(), doubleValue: 8)
        if !moveOnly {
            summary.exerciseTimeGoal = HKQuantity(unit: .minute(), doubleValue: 30)
            summary.standHoursGoal = HKQuantity(unit: .count(), doubleValue: 12)
        }
        return HealthActivityReading(summary)
    }

    static func trendsModel() -> HealthTrendsModel {
        guard let mode = UserDefaults.standard.string(forKey: "showHealthHomeTrendsExample") else { return HealthTrendsModel() }
        return HealthTrendsModel(deps: .init(access: { .granted }, requestAccess: { true }, daily: { metric, days in
            guard mode != "empty" else { return [:] }
            var values: [String: Double] = [:]
            for (i, day) in HealthTrends.dayKeys(days, now: Date()).enumerated() {
                switch metric {
                case .steps: values[day] = Double(5200 + (i * 379) % 4800)
                case .sleep: values[day] = 6.2 + Double(i % 5) * 0.3
                case .weight: values[day] = 68.4 - Double(i % 7) * 0.1
                }
            }
            return values
        }, now: { Date() }))
    }
}

struct HealthTodayExample: View {
    @State private var water = 750
    @State private var protein = 36

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.s) {
            VStack(spacing: Theme.Space.s) {
                GaugeRing(title: String(localized: "Water"), value: water.formatted(), unit: String(localized: "ml"),
                          subtitle: String(localized: "Goal: 2,000 ml"), ratio: Double(water) / 2000, color: Theme.Health.water,
                          subtitleLineLimit: nil)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(Text("Water"))
                    .accessibilityValue(String(localized: "\(water) of 2,000 ml"))
                    .accessibilityIdentifier("health-water-ring")
                Button { water += 250 } label: {
                    Label { Text("Drank a Glass").lineLimit(1).minimumScaleFactor(0.6) } icon: { ThemedIcon(Theme.Icon.water) }
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton()
                Button { water += 100 } label: {
                    Text("Other Amount").lineLimit(1).minimumScaleFactor(0.6)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                    .themedButton()
            }
            .frame(maxWidth: .infinity)
            VStack(spacing: Theme.Space.s) {
                GaugeRing(title: String(localized: "Protein"), value: protein.formatted(), unit: String(localized: "g"),
                          subtitle: String(localized: "Goal: 60 g"), ratio: Double(protein) / 60, color: Theme.Health.protein,
                          subtitleLineLimit: nil)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(Text("Protein"))
                    .accessibilityValue(String(localized: "\(protein) of 60 g"))
                    .accessibilityIdentifier("health-protein-ring")
                Button { protein += 10 } label: {
                    Label { Text("Log Grams").lineLimit(1).minimumScaleFactor(0.6) } icon: { ThemedIcon(Theme.Icon.protein) }
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton()
            }
            .frame(maxWidth: .infinity)
        }
        .font(Theme.Font.label)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("health-today-example")
    }
}
#endif
