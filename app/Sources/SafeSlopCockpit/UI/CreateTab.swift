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
    @State private var scopeExpanded = true
    @State private var selectedProfile = ""
    @State private var presets: [Safeslop_Control_V1_Preset] = []

    /// The merge target: the picked profile, or the only one. Empty when none/invalid.
    private var mergeTarget: String {
        if !selectedProfile.isEmpty && profiles.contains(where: { $0.name == selectedProfile }) { return selectedProfile }
        return profiles.first?.name ?? ""
    }
    /// Merge is allowed only when there's a target and no `files:` block exists yet (avoid a
    /// duplicate-field error; re-scoping an existing block is a manual edit for now).
    private var canMerge: Bool { !mergeTarget.isEmpty && !cueText.contains("files:") }

    var body: some View {
        HSplitView {
            // Left: the canonical CUE text + profile CRUD + preset loader.
            VStack(alignment: .leading, spacing: 6) {
                HStack {
                    Text("safeslop.cue").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                    Spacer()
                    Menu("Profile") {
                        Button("New profile", systemImage: "plus") { newProfile() }
                        if !mergeTarget.isEmpty {
                            Button("Delete \(mergeTarget)", systemImage: "trash", role: .destructive) {
                                deleteProfile(mergeTarget)
                            }
                        }
                    }.menuStyle(.borderlessButton).fixedSize()
                    Menu("Presets") {
                        if presets.isEmpty { Text("none") }
                        ForEach(presets, id: \.name) { p in
                            Button("\(p.name) — \(p.summary)") { cueText = p.cue }
                        }
                    }.menuStyle(.borderlessButton).fixedSize()
                }
                TextEditor(text: $cueText)
                    .font(.system(.body, design: .monospaced))
                    .frame(minWidth: 320, minHeight: 320)
                    .overlay(RoundedRectangle(cornerRadius: 6).stroke(.quaternary))
            }
            .padding(10)

            // Right: file-scope helper (top, open), then live validation + arbiter preview.
            VStack(alignment: .leading, spacing: 10) {
                HStack(spacing: 6) {
                    Image(systemName: statusIcon).foregroundStyle(statusColor)
                    Text(statusText).font(.callout.weight(.medium)).foregroundStyle(statusColor)
                    if validating { ProgressView().controlSize(.small) }
                }

                DisclosureGroup("File scope helper", isExpanded: $scopeExpanded) {
                    VStack(alignment: .leading, spacing: 6) {
                        if profiles.count > 1 {
                            Picker("Target", selection: $selectedProfile) {
                                ForEach(profiles.map(\.name), id: \.self) { Text($0).tag($0) }
                            }
                            .pickerStyle(.menu).font(.caption)
                        }
                        FileScopeEditor(mergeLabel: mergeTarget, mergeEnabled: canMerge, onMerge: merge)
                    }
                    .padding(.top, 4)
                }
                .font(.callout.weight(.medium))

                Divider()

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
                Text("Editing is live-validated. Saving to a repo + the trust gate land next (specs/0029).")
                    .font(.caption2).foregroundStyle(.tertiary)
            }
            .frame(minWidth: 300, maxHeight: .infinity, alignment: .top)
            .padding(10)
        }
        // debounced live validation: .task(id:) cancels + restarts on each keystroke.
        .task(id: cueText) {
            try? await Task.sleep(for: .milliseconds(350))
            if Task.isCancelled { return }
            await validate()
        }
        .task { await loadPresets() } // once, on appear
    }

    private func loadPresets() async {
        guard await EngineConnection.ensureServing() else { return }
        presets = (try? await EngineConnection.listPresets()) ?? []
    }

    /// New profile: insert a deny-network sandbox template into the profiles block (unique name).
    private func newProfile() {
        let existing = Set(profiles.map(\.name))
        var name = "newprofile"
        var n = 1
        while existing.contains(name) { n += 1; name = "newprofile\(n)" }
        let block = "\n\t\t\(name): {agent: \"claude\", environment: \"sandbox\", network: \"deny\"}"
        guard let r = cueText.range(of: "profiles:"),
              let brace = cueText.range(of: "{", range: r.upperBound..<cueText.endIndex) else { return }
        cueText.insert(contentsOf: block, at: brace.upperBound)
        selectedProfile = name
    }

    /// Delete a profile: brace-match its block and remove it (plus its leading whitespace). A targeted
    /// text edit — the rest of the canonical document is untouched.
    private func deleteProfile(_ name: String) {
        guard !name.isEmpty,
              let nameR = cueText.range(of: name + ":"),
              let braceStart = cueText.range(of: "{", range: nameR.upperBound..<cueText.endIndex) else { return }
        var depth = 0
        var idx = braceStart.lowerBound
        var end: String.Index?
        while idx < cueText.endIndex {
            switch cueText[idx] {
            case "{": depth += 1
            case "}":
                depth -= 1
                if depth == 0 { end = cueText.index(after: idx) }
            default: break
            }
            if end != nil { break }
            idx = cueText.index(after: idx)
        }
        guard let endIdx = end else { return }
        var start = nameR.lowerBound
        while start > cueText.startIndex {
            let p = cueText.index(before: start)
            if cueText[p] == "\n" || cueText[p] == "\t" || cueText[p] == " " { start = p } else { break }
        }
        cueText.removeSubrange(start..<endIdx)
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
                if !profiles.contains(where: { $0.name == selectedProfile }) {
                    selectedProfile = profiles.first?.name ?? ""
                }
            } else {
                error = resp.error
                profiles = []
            }
        } catch {
            self.error = "validate failed: \(error)"
            profiles = []
        }
    }

    /// Splice a generated `files:` block into the target profile's CUE block, right after its opening
    /// brace. Guarded by canMerge (no existing files:), so this can't create a duplicate field. A
    /// targeted text edit — it leaves the rest of the canonical text untouched.
    private func merge(_ snippet: String) {
        let name = mergeTarget
        guard !name.isEmpty,
              let nameRange = cueText.range(of: name + ":"),
              let braceRange = cueText.range(of: "{", range: nameRange.upperBound..<cueText.endIndex)
        else { return }
        let indented = "\n\t\t\t" + snippet.replacingOccurrences(of: "\n", with: "\n\t\t\t")
        cueText.insert(contentsOf: indented, at: braceRange.upperBound)
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
