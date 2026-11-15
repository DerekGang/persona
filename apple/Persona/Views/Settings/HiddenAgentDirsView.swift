import SwiftUI

/// Settings owned by the selected Assistant, independent of runtime and available for offline computers too.
struct HiddenAgentDirsView: View {
    @Environment(AppModel.self) private var model
    private let scope = AssistantSettingsRequestScope(AppModel.shared)
    @State private var state: HiddenAgentDirs?
    @State private var rows: [HiddenAgentDirRule] = []
    @State private var machine = ""
    @State private var pattern = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        SettingsForm {
            Section {
                Picker(String(localized: "picker.machine", defaultValue: "Machine"), selection: $machine) {
                    ForEach(scope.machines(model), id: \.id) { m in Text(m.title).tag(m.id) }
                }
            }
            Section {
                if let error {
                    Text(error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                    if state == nil {
                        Button("Retry [Components b8784c]") { Task { await load() } }.formButton().lineLimit(1)
                    }
                }
                if state != nil {
                    ForEach(rows) { rule in
                        HStack(spacing: Theme.Space.m) {
                            Toggle(isOn: Binding(get: { rule.enabled }, set: { enabled in
                                var replacement = rule
                                replacement.enabled = enabled
                                if let state { Task { await save(state.replacing(rule, with: replacement)) } }
                            })) {
                                Text(verbatim: rule.pattern).font(Theme.Font.code).lineLimit(1).truncationMode(.middle)
                            }
                            Button("Delete", role: .destructive) {
                                if let state { Task { await save(state.replacing(rule, with: nil)) } }
                            }.formButton().lineLimit(1)
                        }
                    }
                    TextField("Folder path or glob", text: $pattern)
                        .font(Theme.Font.code)
                        .autocorrectionDisabled()
                        #if os(iOS) || os(visionOS)
                        .textInputAutocapitalization(.never)
                        #endif
                    Button("Add hidden folder") { Task { await add() } }
                        .formButton().lineLimit(1)
                        .disabled(pattern.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || machine.isEmpty)
                } else if error == nil {
                    ProgressView()
                }
            } header: {
                Text("Hidden Agent Folders")
            } footer: {
                Text("Matches stay recorded but leave Agents and Assistant observations. Use a folder path or glob; ~ means this computer's home. Turn a rule off to restore sessions. Active supervised tasks remain visible. Show hidden sessions in the Agents menu temporarily.")
            }
            .disabled(busy)
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "Hidden Agent Folders"))
        .task { await load() }
        .onChange(of: machine) { _, _ in updateRows() }
    }

    private func updateRows() { rows = state?.rules(on: machine) ?? [] }

    private func load() async {
        guard let api = scope.api(model) else { error = ClaudeDirsLogic.noHub; return }
        busy = true
        defer { busy = false }
        do {
            state = try await api.hiddenAgentDirs()
            if machine.isEmpty { machine = scope.machines(model).first?.id ?? "" }
            updateRows()
            error = nil
        } catch {
            if !error.isCancellation { self.error = error.localizedDescription }
        }
    }

    private func add() async {
        guard var next = state else { return }
        let clean = pattern.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !machine.isEmpty, !clean.isEmpty else { return }
        let rule = HiddenAgentDirRule(machineID: machine, pattern: clean, enabled: true)
        guard !next.rules.contains(where: { $0.id == rule.id }) else { return }
        next.rules.append(rule)
        if await save(next) { pattern = "" }
    }

    @discardableResult private func save(_ next: HiddenAgentDirs) async -> Bool {
        guard !busy, let api = scope.api(model), let previous = state else { return false }
        busy = true
        state = next
        updateRows()
        error = nil
        defer { busy = false }
        do {
            state = try await api.setHiddenAgentDirs(next)
            updateRows()
            Haptics.success()
            return true
        } catch {
            state = previous
            updateRows()
            if !error.isCancellation { self.error = error.localizedDescription; Haptics.error() }
            return false
        }
    }
}
