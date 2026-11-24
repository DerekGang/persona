import SwiftUI

nonisolated struct CommandInputDetails {
    var command: String
    var output: String
    var prompt: String
    var masked: Bool
}

extension Interaction {
    var commandInput: CommandInputDetails? {
        guard kind == .permission, toolName == "command_input", let payload = toolInput?.object else { return nil }
        return CommandInputDetails(command: payload["command"]?.string ?? "", output: payload["output"]?.string ?? "",
                                   prompt: payload["prompt"]?.string ?? fields?.first(where: { $0.key == "input" })?.label ?? "",
                                   masked: payload["masked"].map { $0.bool ?? true } ?? false)
    }
}

struct CommandInputForm: View {
    @Environment(AppModel.self) private var model
    @Environment(\.openLink) private var openLink
    @Environment(\.dynamicTypeSize) private var dynamicType
    @Environment(\.scenePhase) private var scenePhase
    var interaction: Interaction
    var details: CommandInputDetails
    @State private var line = ""
    @State private var sending = false
    @State private var refusal: String?
    @State private var link: LinkTarget?

    private var prompt: String { details.prompt.isEmpty ? String(localized: "Input") : details.prompt }
    private var canSend: Bool { !line.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text("Command input").font(Theme.Font.heading).accessibilityAddTraits(.isHeader)
            if !details.command.isEmpty {
                Text(details.command).font(Theme.Font.label.monospaced())
            }
            if !details.output.isEmpty {
                Text(ExchangeText.linkWebAddresses(AttributedString(details.output)))
                    .font(Theme.Font.label.monospaced())
                    .foregroundStyle(.secondary)
            }
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                Text(prompt).font(Theme.Font.labelStrong)
                SecretField(prompt, text: $line, identifier: "interaction.field.input", startsHidden: details.masked)
                    .interactionField()
            }
            if sending { Text("Sending…").font(Theme.Font.label).foregroundStyle(.secondary) }
            if let refusal {
                Text(refusal).font(Theme.Font.label).foregroundStyle(Theme.cautionText)
                    .accessibilityIdentifier("interaction.commandInput.refusal")
            }
            let layout = dynamicType.isAccessibilitySize ? AnyLayout(VStackLayout(spacing: Theme.Space.s))
                : AnyLayout(HStackLayout(spacing: Theme.Space.m))
            layout { replyButtons }
        }
        .disabled(sending)
        .environment(\.openURL, OpenURLAction { url in
            guard ["https", "http"].contains(url.scheme?.lowercased() ?? "") else { return .discarded }
            if let openLink { openLink(url) }
            else { link = LinkTarget(url: url, manager: interaction.manager) }
            return .handled
        })
        .sheet(item: $link) { target in LinkSheet(target: target, key: interaction.isSessionless ? nil : interaction.sessionKey) }
        .onChange(of: scenePhase) { _, phase in if phase == .background { line = "" } }
        .onDisappear { line = "" }
    }

    @ViewBuilder private var replyButtons: some View {
        Button("Not now") { submit(allow: false) }
            .themedButton()
            .accessibilityIdentifier("interaction.commandInput.defer")
        Button("Send") { submit(allow: true) }
            .themedButton(prominent: true)
            .disabled(!canSend)
            .accessibilityIdentifier("interaction.commandInput.send")
    }

    private func submit(allow: Bool) {
        guard !sending, !allow || canSend else { return }
        sending = true
        refusal = nil
        let resolution = Resolution(allow: allow, values: allow ? ["input": line] : nil)
        line = ""
        Task {
            await model.resolve(interaction, resolution, onCardFailure: { refusal = $0 })
            sending = false
        }
    }
}

struct PermissionInputForm: View {
    @Environment(AppModel.self) private var model
    var interaction: Interaction
    @State private var values: [String: String] = [:]
    @State private var sending = false

    private var complete: Bool {
        (interaction.fields ?? []).allSatisfy {
            $0.optional == true || !(values[$0.key] ?? "").trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            Text(interaction.summary ?? String(localized: "Confirm settings change"))
                .font(Theme.Font.secondary)
            ForEach(interaction.fields ?? []) { field in
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    Text(field.label).font(Theme.Font.labelStrong)
                    Group {
                        if field.secret == true {
                            // A key or a password handed to the Agent, not this app's: never offered to the system's
                            // passwords (#1598).
                            SecretField(field.placeholder ?? field.label, filled: String(localized: "Filled in"), text: binding(field.key),
                                        identifier: "interaction.field." + field.key)
                                .onSubmit { submit(allow: true) }
                        } else {
                            TextField(field.placeholder ?? field.label, text: binding(field.key))
                                .onSubmit { submit(allow: true) }
                        }
                    }
                    .interactionField()
                    .accessibilityIdentifier("interaction.field." + field.key)
                }
            }
            HStack(spacing: Theme.Space.m) {
                Button { submit(allow: false) } label: { Text("Deny").lineLimit(1).frame(maxWidth: .infinity) }
                    .themedButton()
                Button { submit(allow: true) } label: { Text("Fill In & Allow").lineLimit(1).frame(maxWidth: .infinity) }
                    .themedButton(prominent: true)
                    .disabled(!complete)
            }
            .disabled(sending)
        }
        .onDisappear { values = [:] }
        .onChange(of: interaction.id) { values = [:] }
    }

    private func binding(_ key: String) -> Binding<String> {
        Binding(get: { values[key] ?? "" }, set: { values[key] = $0 })
    }

    private func submit(allow: Bool) {
        guard !sending, !allow || complete else { return }
        sending = true
        let resolution = Resolution(allow: allow, values: allow ? values : nil)
        values = [:]
        Task {
            await model.resolve(interaction, resolution)
            sending = false
        }
    }
}

extension View {
    /// A card's text field (`PermissionInputForm`, `SecretRunForm`): body text on the well, as typed.
    func interactionField() -> some View {
        font(Theme.Font.body)
            .textFieldStyle(.plain)
            .autocorrectionDisabled()
            .padding(Theme.Space.m)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control))
    }
}
