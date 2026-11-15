import Foundation

/// Visibility rules are evaluated by the manager, against each computer's home and session working directory.
nonisolated struct HiddenAgentDirRule: Codable, Equatable, Identifiable, Sendable {
    var machineID: String
    var pattern: String
    var enabled: Bool
    var id: String { machineID + "\n" + pattern }
    enum CodingKeys: String, CodingKey { case machineID = "machine_id", pattern, enabled }
}

nonisolated struct HiddenAgentDirs: Codable, Equatable, Sendable {
    var rules: [HiddenAgentDirRule] = []

    func rules(on machine: String) -> [HiddenAgentDirRule] { rules.filter { $0.machineID == machine } }

    func replacing(_ rule: HiddenAgentDirRule, with replacement: HiddenAgentDirRule?) -> Self {
        Self(rules: rules.compactMap { $0.id == rule.id ? replacement : $0 })
    }
}

extension APIClient {
    func hiddenAgentDirs() async throws -> HiddenAgentDirs {
        try await getDecodedOffMain("/api/v1/assistant/hidden-agent-dirs")
    }

    func setHiddenAgentDirs(_ value: HiddenAgentDirs) async throws -> HiddenAgentDirs {
        let data = try await request("PUT", "/api/v1/assistant/hidden-agent-dirs", body: value)
        return try await Task.detached(priority: .userInitiated) {
            try JSON.decoder.decode(HiddenAgentDirs.self, from: data)
        }.value
    }
}
