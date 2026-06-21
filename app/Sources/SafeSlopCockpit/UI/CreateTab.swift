import SwiftUI

/// CreateTab is the profile editor. Per the research, CUE text is the canonical source of truth; this
/// is the text side, with live validation + a live arbiter preview so the user sees what each profile
/// MEANS as they type (cue-vet errors inline, break-glass consequences per profile). The visual
/// (form) side — a lossless safe subset that locks on advanced constructs — is the next task and is
/// gated on a design decision (specs/0029 FLO hand-off), so it's intentionally not here yet.
struct CreateTab: View {
    @State private var cueText = CreateTab.starter
    @State private var error: String?
    @State private var profiles: [ProfileRef] = []
    @State private var validating = false

    var body: some View {
        HSplitView {
            // Left: the canonical CUE text.
            VStack(alignment: .leading, spacing: 6) {
                Text("safeslop.cue").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                TextEditor(text: $cueText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minWidth: 320, minHeight: 320)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
            }
            .padding(10)

            // Right: live validation + arbiter preview.
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 6) {
                    Image(systemName: statusIcon).foregroundStyle(statusColor)
                    Text(statusText).font(.callout.weight(.medium)).foregroundStyle(statusColor)
                    if validating { ProgressView().controlSize(.small) }
                }
                if let error {
                    ScrollView {
                        Text(error).font(.caption.monospaced()).foregroundStyle(.red)
                            .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                    }
                } else {
                    ScrollView {
                        VStack(alignment: .leading, spacing: 12) {
                            ForEach(profiles) { ref in
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(ref.name).font(.headline)
                                    ArbiterPane(ref: ref)
                                }
                                .padding(10)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .background(ref.riskColor.opacity(0.07), in: RoundedRectangle(cornerRadius: 8))
                            }
                        }
                    }
                }
                DisclosureGroup("File scope helper") {
                    FileScopeEditor()
                }
                .font(.callout.weight(.medium))

                Spacer()
                Text("Editing is live-validated. Saving to a repo + the trust gate land next (specs/0029).")
                    .font(.caption2).foregroundStyle(.tertiary)
            }
            .frame(minWidth: 280)
            .padding(10)
        }
        // debounced live validation: .task(id:) cancels + restarts on each keystroke.
        .task(id: cueText) {
            try? await Task.sleep(for: .milliseconds(350))
            if Task.isCancelled { return }
            await validate()
        }
    }

    private var statusIcon: String {
        if error != nil { return "xmark.octagon.fill" }
        return profiles.isEmpty ? "doc.text" : "checkmark.seal.fill"
    }
    private var statusColor: Color { error != nil ? .red : (profiles.isEmpty ? .secondary : .green) }
    private var statusText: String {
        if error != nil { return "invalid" }
        return profiles.isEmpty ? "no profiles" : "\(profiles.count) profile\(profiles.count == 1 ? "" : "s") — valid"
    }

    private func validate() async {
        guard await EngineConnection.ensureServing() else { error = "engine unreachable"; return }
        validating = true
        defer { validating = false }
        do {
            let resp = try await EngineConnection.validatePolicy(cueText)
            if resp.valid {
                error = nil
                profiles = resp.profiles.map(ProfileRef.init)
            } else {
                error = resp.error
                profiles = []
            }
        } catch {
            self.error = "validate failed: \(error)"
            profiles = []
        }
    }

    /// A non-blank starter (research: never start with a blank editor) — a deny-network sandbox, the
    /// safe default, ready to edit.
    static let starter = """
    package safeslop

    safeslop: {
    \tversion: 1
    \tprofiles: {
    \t\t// edit me — a deny-network sandbox is the safe default
    \t\tdev: {agent: "claude", environment: "sandbox", network: "deny"}
    \t}
    }
    """
}
