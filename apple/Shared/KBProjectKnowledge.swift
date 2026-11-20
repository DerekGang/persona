import Foundation

/// Project-local evidence positions are independent of the date on the entity. Older Hubs omit this object.
nonisolated struct KBProjectKnowledge: Codable, Hashable, Sendable {
    var policy: String
    var position: Int
    var needsReview: Bool
    var fields: [String: Field]
    var sources: [Source]?

    enum CodingKeys: String, CodingKey {
        case policy, position, fields, sources
        case needsReview = "needs_review"
    }

    nonisolated struct Field: Codable, Hashable, Sendable {
        var status: String
        var cutoff: Int?
        var uncovered: Int?
        var writer: String?
        var maintainer: String?
        var locked: Bool?
        var sourceRefs: [String]?

        enum CodingKeys: String, CodingKey {
            case status, cutoff, uncovered, writer, maintainer, locked
            case sourceRefs = "source_refs"
        }
    }

    nonisolated struct Source: Codable, Identifiable, Hashable, Sendable {
        var ref: String
        var state: String?
        var stateAt: Date?
        var id: String { ref }

        enum CodingKeys: String, CodingKey {
            case ref, state
            case stateAt = "state_at"
        }
    }
}

nonisolated enum KBProjectPolicy: String, Sendable {
    case managed
    case referenceOnly = "reference_only"
}

/// Only owner-supplied pointers can be replaced. Sources discovered through chats, receipts and history stay on the Hub.
nonisolated struct KBProjectStorageDraft {
    private let savedPolicy: KBProjectPolicy
    private let savedRefs: [String]
    var policy: KBProjectPolicy
    var references: String

    init?(_ detail: KBDetail) {
        guard detail.kind == KBKind.projects.rawValue, let knowledge = detail.projectKnowledge,
              let policy = KBProjectPolicy(rawValue: knowledge.policy) else { return nil }
        let refs = detail.meta?["source_refs"]?.array?.compactMap(\.string) ?? []
        savedPolicy = policy
        savedRefs = refs
        self.policy = policy
        references = refs.joined(separator: "\n")
    }

    var update: KBUpdate? {
        let refs = references.components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
        guard policy != savedPolicy || refs != savedRefs else { return nil }
        return KBUpdate(storagePolicy: policy == savedPolicy ? nil : policy.rawValue, sourceRefs: refs == savedRefs ? nil : refs)
    }
}

extension APIClient {
    /// Use the client that read the project, including its credentials and manager, only while this page still owns it.
    func saveProjectStorage(_ draft: KBProjectStorageDraft, id: String, current: APIClient?) async throws {
        guard sameSettingsScope(as: current), !Task.isCancelled else { throw CancellationError() }
        if let update = draft.update { try await kbUpdate(KBKind.projects.rawValue, id: id, update) }
    }
}
