import SwiftUI

/// HostConsentView is the in-window host-launch comprehension gate (specs/0030): a non-collapsible
/// honesty headline (always true, never answerable), a per-launch live scope line, then engine-authored
/// statements the user must match against ground truth. The "Launch as me" button is disabled — and is
/// the Return default — only once every row matches; a wrong toggle disarms and clears all (via
/// HostConsentModel). Arming then Touch ID authorizes; Cancel/Esc returns to the list with nothing
/// granted. Rendered by swapping window content (a Phase case), so the only system-modal surface in the
/// whole flow is the OS Touch ID dialog — no modal-on-modal.
struct HostConsentView: View {
    let ref: ProfileRef
    let onLaunch: () -> Void
    let onCancel: () -> Void
    @State private var model: HostConsentModel

    init(ref: ProfileRef, preflight: Preflight, onLaunch: @escaping () -> Void, onCancel: @escaping () -> Void) {
        self.ref = ref
        self.onLaunch = onLaunch
        self.onCancel = onCancel
        _model = State(initialValue: HostConsentModel(preflight))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            // READ — the honesty anchor (fixed, always true, never an answerable row).
            VStack(alignment: .leading, spacing: 6) {
                Label("Launch host profile “\(ref.name)”", systemImage: "exclamationmark.octagon.fill")
                    .font(.title3.weight(.semibold)).foregroundStyle(.red)
                Text(model.headlineBody).font(.callout)
                Text(model.scopeLine).font(.callout.weight(.medium)).foregroundStyle(.secondary)
            }
            .padding(12)
            .frame(maxWidth: .infinity, alignment: .leading)
            .background(.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))

            // MATCH — the comprehension act.
            Text("Confirm you understand — answer each line:").font(.callout.weight(.medium))
            ForEach(model.statements) { s in
                HStack(spacing: 12) {
                    Text(s.text).font(.callout)
                        .foregroundStyle(model.wrongRowID == s.id ? Color.red : Color.primary)
                    Spacer()
                    Picker("", selection: answerBinding(for: s)) {
                        Text("No").tag(Optional(false))
                        Text("Yes").tag(Optional(true))
                    }
                    .pickerStyle(.segmented).labelsHidden().fixedSize()
                }
                .padding(.vertical, 2)
            }
            if model.wrongRowID != nil {
                Text("That one's wrong — re-read the highlighted line.")
                    .font(.caption).foregroundStyle(.red)
            }

            // AUTHORIZE / abort — symmetric, one action each, both always visible.
            HStack {
                Button("Cancel", role: .cancel, action: onCancel)
                Spacer()
                Button("Launch as me", action: { Task { await authorize() } })
                    .buttonStyle(.borderedProminent).tint(.red)
                    .disabled(!model.allMatched)
                    .keyboardShortcut(model.allMatched ? .defaultAction : nil)
            }
        }
        .padding(20)
        .frame(maxWidth: 520)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private func answerBinding(for s: ConsentStatement) -> Binding<Bool?> {
        Binding(
            get: { model.statements.first(where: { $0.id == s.id })?.answer ?? nil },
            set: { if let v = $0 { model.answer(s.id, v) } }
        )
    }

    private func authorize() async {
        // Comprehension was already proven by arming; Touch ID is identity, not comprehension. On
        // cancel/fail we stay in MATCH still armed (no re-shuffle) — the user can retry the biometric.
        let ok = await BiometricGate.confirm(
            reason: "Authorize launching host profile “\(ref.name)” as you, with no isolation.")
        if ok { onLaunch() }
    }
}

/// HostConsentPreview renders HostConsentView with a hardcoded sample payload for the screenshot harness
/// only (COCKPIT_PREVIEW=host-consent) — the live launch flow never reaches it. It exists so
/// `make cockpit-shot host-consent` can capture the gate's layout headlessly, the same self-test
/// affordance COCKPIT_TAB gives the three main tabs (specs/0030).
struct HostConsentPreview: View {
    /// True when the screenshot harness asked for the host-consent layout (never in normal use).
    static var isActive: Bool {
        ProcessInfo.processInfo.environment["COCKPIT_PREVIEW"]?.lowercased() == "host-consent"
    }

    private static let sampleRef = ProfileRef(name: "risky", agent: "claude", environment: "host",
                                              network: "allow", tier: "none", riskLevel: "high")
    private static let sample = Preflight(
        headlineBody: "This agent runs on your Mac as you — no isolation. It can read and write every "
            + "file your account can, use your logged-in credentials, and reach any network your Mac can "
            + "reach. Nothing about profile \"risky\" is sandboxed.",
        scopeLine: "This run: your home folder + 2 other mounted volumes, 1 safeslop-injected credential, full host network.",
        statements: [
            ConsentStatement(text: "Network access is limited to an approved allow-list.", expected: false, tierOrigin: "container"),
            ConsentStatement(text: "This agent can read and write every file your account can.", expected: true, tierOrigin: "host"),
            ConsentStatement(text: "Files outside the project are invisible to this agent.", expected: false, tierOrigin: "vm"),
        ])

    var body: some View {
        HostConsentView(ref: Self.sampleRef, preflight: Self.sample, onLaunch: {}, onCancel: {})
            .frame(width: 520, height: 460)
    }
}
