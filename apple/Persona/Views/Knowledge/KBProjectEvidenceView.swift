import SwiftUI

/// A field can miss the same evidence as its neighbor. Keep the field names and counts together.
struct KBProjectReviewNote: View {
    var knowledge: KBProjectKnowledge?

    var body: some View {
        if let knowledge, knowledge.needsReview {
            let notes = ["summary", "one_line"].compactMap { key -> String? in
                guard let field = knowledge.fields[key], field.status == "needs_review" else { return nil }
                let title = key == "summary" ? String(localized: "Summary needs review") : String(localized: "Overview needs review")
                guard let n = field.uncovered, n > 0 else { return title }
                return title + " · " + Self.newerUpdates(n)
            }
            Label {
                Text(notes.isEmpty ? String(localized: "Needs review") : notes.joined(separator: "\n"))
                    .fixedSize(horizontal: false, vertical: true)
            } icon: {
                ThemedIcon(Theme.Icon.time).foregroundStyle(Theme.Hue.orange)
            }
            .font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }
    static func newerUpdates(_ n: Int) -> String {
        n == 1 ? String(localized: "1 newer update") : String(localized: "\(n) newer updates")
    }
}

struct KBProjectEvidenceSection: View {
    var detail: KBDetail
    var knowledge: KBProjectKnowledge
    var api: APIClient
    var saved: () async -> Void
    @State private var expanded = false
    @State private var editing = false

    var body: some View {
        Section {
            Button { expanded.toggle() } label: {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
                    Label("Sources and coverage", systemImage: Theme.Icon.knowledge)
                    Spacer(minLength: Theme.Space.xs)
                    if knowledge.policy == KBProjectPolicy.referenceOnly.rawValue {
                        Text("References only").font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.caption).foregroundStyle(.secondary)
                        .rotationEffect(.degrees(expanded ? 90 : 0)).accessibilityHidden(true)
                }
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityValue(expanded ? Text("Expanded") : Text("Collapsed"))
            .accessibilityIdentifier("kb.project.evidence")

            if expanded {
                ForEach(["summary", "one_line"], id: \.self) { key in
                    coverage(key == "summary" ? String(localized: "Summary") : String(localized: "One-line overview"), knowledge.fields[key])
                }
                if let policy = KBProjectPolicy(rawValue: knowledge.policy) {
                    Button { editing = true } label: {
                        VStack(alignment: .leading, spacing: Theme.Space.xs) {
                            Label("Edit project storage", systemImage: Theme.Icon.edit)
                            Text(policy == .referenceOnly ? String(localized: "References only") : String(localized: "Managed by Persona"))
                                .font(Theme.Font.caption).foregroundStyle(.secondary)
                        }
                    }
                    .accessibilityIdentifier("kb.project.edit")
                    .sheet(isPresented: $editing) {
                        if let draft = KBProjectStorageDraft(detail) {
                            KBProjectStorageEditor(draft: draft, id: detail.id, api: api, saved: saved)
                        }
                    }
                }
            }
        }
        if expanded, let sources = knowledge.sources, !sources.isEmpty {
            Section {
                ForEach(sources) { source in
                    VStack(alignment: .leading, spacing: Theme.Space.xs) {
                        sourceReference(source.ref)
                        if let state = source.state, !state.isEmpty {
                            Text("Source status: \(sourceStatus(state))").font(Theme.Font.caption).foregroundStyle(.secondary)
                            if let at = source.stateAt { Text(at.formatted(date: .abbreviated, time: .shortened)).font(Theme.Font.label).foregroundStyle(.secondary) }
                        }
                    }
                    .padding(.vertical, Theme.Space.xxs)
                }
            } header: {
                Text("Sources")
            } footer: {
                if sources.contains(where: { $0.state?.isEmpty == false }) {
                    Text("Deployment and acceptance need their own evidence.")
                }
            }
        }
    }

    private func coverage(_ title: String, _ field: KBProjectKnowledge.Field?) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(title).font(Theme.Font.secondaryStrong)
            if field?.locked == true { Label("Locked against automatic updates", systemImage: Theme.Icon.locked).font(Theme.Font.caption) }
            if let cutoff = field?.cutoff {
                Text("Evidence covered: \(cutoff) of \(knowledge.position)")
            } else {
                Text("Coverage unknown")
            }
            if let n = field?.uncovered, n > 0 { Text(KBProjectReviewNote.newerUpdates(n)) }
            Text(responsibility(field?.writer, writer: true))
            Text(responsibility(field?.maintainer, writer: false))
            if let refs = field?.sourceRefs, !refs.isEmpty {
                Text("Based on: \(refs.joined(separator: " · "))").textSelection(.enabled)
            }
        }
        .font(Theme.Font.caption).foregroundStyle(.secondary)
        .padding(.vertical, Theme.Space.xxs)
        .accessibilityElement(children: .combine)
    }

    private func responsibility(_ value: String?, writer: Bool) -> String {
        switch value {
        case "user": return writer ? String(localized: "Written by you") : String(localized: "Maintained by you")
        case "pipeline": return writer ? String(localized: "Written by Persona") : String(localized: "Maintained by Persona")
        case "external" where !writer: return String(localized: "Maintained at the original source")
        case let value? where value.hasPrefix("ext:") && value.count > 4:
            let name = String(value.dropFirst(4))
            return writer ? String(localized: "Written by \(name)") : String(localized: "Maintained by \(name)")
        default: return writer ? String(localized: "Writer unknown") : String(localized: "Maintainer unknown")
        }
    }

    private func sourceStatus(_ value: String) -> String {
        switch value {
        case "merged": String(localized: "Merged [Project source]")
        case "closed": String(localized: "Closed [Project source]")
        case "open": String(localized: "Open [Project source]")
        default: value
        }
    }

    @ViewBuilder private func sourceReference(_ ref: String) -> some View {
        let parts = ref.split(separator: "/", omittingEmptySubsequences: false)
        if let url = URL(string: ref), ["http", "https"].contains(url.scheme?.lowercased()), url.host() != nil, url.user() == nil {
            Link(ref, destination: url)
        } else if parts.count == 2, parts[0] == "moments", let moment = detail.moments?.first(where: { $0.id == parts[1] }) {
            NavigationLink { KBMomentView(ref: moment) } label: { Text(ref) }
        } else if parts.count == 2, !parts[1].isEmpty, KBKind(rawValue: String(parts[0])) != nil {
            let linked = detail.links?.first { $0.kind == parts[0] && $0.id == parts[1] }
            let entry = linked ?? KBEntry(kind: String(parts[0]), id: String(parts[1]), name: ref, relevance: 0)
            KBEntryLink(entry: entry) {
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(entry.name)
                    if entry.name != ref { Text(ref).font(Theme.Font.caption).foregroundStyle(.secondary) }
                }
            }
        } else {
            Text(ref).textSelection(.enabled)
        }
    }
}

private struct KBProjectStorageEditor: View {
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    @State var draft: KBProjectStorageDraft
    var id: String
    var api: APIClient
    var saved: () async -> Void
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    Toggle("References only", isOn: Binding(get: { draft.policy == .referenceOnly }, set: {
                        draft.policy = $0 ? .referenceOnly : .managed
                    })).accessibilityIdentifier("kb.project.reference-only")
                } footer: {
                    if draft.policy == .referenceOnly {
                        Text("Keep links to the originals. Existing notes stay here. New summaries, overviews and timeline entries are blocked.")
                    } else {
                        Text("Persona can update the overview, summary and timeline. Locked fields stay as written.")
                    }
                }
                Section {
                    TextField("Source references", text: $draft.references, axis: .vertical)
                        .lineLimit(2...6)
                        .autocorrectionDisabled()
                        .textInputAutocapitalization(.never)
                        .accessibilityIdentifier("kb.project.pointers")
                } header: {
                    Text("Source references")
                } footer: {
                    Text("One web address or knowledge entry per line, such as chats/planning.")
                }
                if let error { Text(error).foregroundStyle(Theme.danger).accessibilityIdentifier("kb.project.error") }
            }
            .disabled(saving)
            .navigationTitle("Edit project storage")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                SheetClose(disabled: saving) { dismiss() }
                SheetConfirm(id: "kb.project.save", name: String(localized: "Save"),
                             disabled: draft.update == nil || !api.sameSettingsScope(as: model.api), busy: saving) { Task { await save() } }
            }
        }
        .interactiveDismissDisabled(saving)
        #if os(macOS)
        .macSheet()
        #endif
    }

    private func save() async {
        guard !saving, draft.update != nil, api.sameSettingsScope(as: model.api) else { return }
        saving = true
        defer { saving = false }
        do {
            try await api.saveProjectStorage(draft, id: id, current: model.api)
            guard api.sameSettingsScope(as: model.api) else { return }
            await saved()
            if api.sameSettingsScope(as: model.api) { dismiss() }
        } catch {
            guard api.sameSettingsScope(as: model.api) else { return }
            self.error = error.shown
        }
    }
}
