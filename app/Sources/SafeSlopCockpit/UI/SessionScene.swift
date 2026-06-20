import SwiftUI

/// A Codable handle for a profile, used as the value type for the per-session WindowGroup so a
/// session window can be reopened/restored. Mirrors the fields of Safeslop_Control_V1_Profile.
struct ProfileRef: Codable, Hashable, Identifiable {
    var name: String
    var agent: String
    var environment: String
    var network: String
    var id: String { name }

    init(name: String, agent: String, environment: String, network: String) {
        self.name = name; self.agent = agent; self.environment = environment; self.network = network
    }
    init(_ p: Safeslop_Control_V1_Profile) {
        self.init(name: p.name, agent: p.agent, environment: p.environment, network: p.network)
    }
    var proto: Safeslop_Control_V1_Profile {
        .with { $0.name = name; $0.agent = agent; $0.environment = environment; $0.network = network }
    }

    /// Trust color (specs/0014 §5): vm/container = amber; else red for open egress, green for deny.
    var trustColor: Color {
        if environment == "vm" || environment == "container" { return .orange }
        return network == "allow" ? .red : .green
    }
}

/// SessionHostView owns the CockpitSession for a window and renders the cockpit chrome.
struct SessionHostView: View {
    let ref: ProfileRef
    @State private var session: CockpitSession

    init(ref: ProfileRef) {
        self.ref = ref
        _session = State(initialValue: CockpitSession(profile: ref.proto))
    }

    var body: some View {
        CockpitChrome(ref: ref, session: session)
            .onDisappear { session.close() }
            .navigationTitle("\(ref.name) — safeslop")
    }
}

/// CockpitChrome = the decorated frame around the embedded terminal: a trust-colored border plus a
/// slim header (identity) and footer (status). The terminal fills the center (specs/0014 §5).
struct CockpitChrome: View {
    let ref: ProfileRef
    let session: CockpitSession
    private let border: CGFloat = 3
    // The trust signal is HOST-DRAWN chrome: the agent owns only the SwiftTerm buffer and cannot
    // paint these views, so it can't fake a "safe" posture (specs/research/2026-06-20-cockpit-safe-
    // by-design.md). A thin border stops registering after ~30 min (the TCC-dot effect), so the
    // trust color is rendered as an AMBIENT tint — the header/footer materials and a faint wash over
    // the terminal — so it keeps feeling different. Tune these opacities to taste:
    private let barTint: Double = 0.18      // trust color over the header/footer bar material
    private let ambientTint: Double = 0.05  // faint "lights on" wash over the terminal

    var body: some View {
        VStack(spacing: 0) {
            header
            TerminalBridge(session: session)
                .frame(minWidth: 480, minHeight: 280)
                // allowsHitTesting(false): never intercept keystrokes meant for the PTY.
                .overlay(ref.trustColor.opacity(ambientTint).allowsHitTesting(false))
            footer
        }
        .padding(border)
        .background(ref.trustColor)
        .overlay {
            if case .needsTrust(let msg) = session.state {
                TrustSheet(ref: ref, message: msg, session: session)
            }
        }
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text(ref.name).font(.headline)
            badge(ref.environment, .secondary)
            badge("net: \(ref.network)", ref.network == "allow" ? .red : .green)
            Spacer()
            Text("agent: \(ref.agent)").font(.caption).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(ref.trustColor.opacity(barTint))
        .background(.bar)
    }

    private var footer: some View {
        HStack {
            Circle().fill(statusColor).frame(width: 8, height: 8)
            Text(statusText).font(.caption).foregroundStyle(.secondary)
            Spacer()
            if !session.sessionID.isEmpty {
                Text(session.sessionID).font(.caption.monospaced()).foregroundStyle(.tertiary)
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 4)
        .background(ref.trustColor.opacity(barTint))
        .background(.bar)
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.semibold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }

    private var statusColor: Color {
        switch session.state {
        case .opening: return .yellow
        case .needsTrust: return .orange
        case .running: return .green
        case .closed: return .gray
        case .error: return .red
        }
    }

    private var statusText: String {
        switch session.state {
        case .opening: return "opening…"
        case .needsTrust: return "needs trust"
        case .running: return "running"
        case .closed: return session.exitCode.map { "exited (\($0))" } ?? "closed"
        case .error(let e): return "error: \(e)"
        }
    }
}

/// TrustSheet covers the terminal *in place* (not a sibling modal — specs/research/
/// 2026-06-20-cockpit-safe-by-design.md) when the engine refused an untrusted/changed safeslop.cue.
/// It states the profile's capabilities in plain language (not CUE), names the highest-risk one in
/// the approve button, and calls the Trust RPC on approval.
struct TrustSheet: View {
    let ref: ProfileRef
    let message: String
    let session: CockpitSession

    private var openEgress: Bool { ref.network == "allow" }
    /// The policy changed since it was trusted (an agent may have edited it) — higher risk than a
    /// first-time approval, so the approve action is gated behind Touch ID.
    private var edited: Bool { message.contains("changed since you trusted") }
    private var buttonTitle: String {
        if edited { return "Re-trust edited policy (Touch ID)" }
        return openEgress ? "Trust & Launch — allows open network" : "Trust & Launch"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label("Review & trust this profile", systemImage: "lock.shield")
                .font(.title3.weight(.semibold))
            Text("safeslop won't run this repo's policy until you approve it. Review what “\(ref.name)” can do, then trust it.")
                .font(.callout).foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 8) {
                capability("cube", "Isolation", ref.environment, .secondary)
                if openEgress {
                    capability("network", "Network", "open — the agent can reach the internet", .red)
                } else {
                    capability("network.slash", "Network", "denied — offline", .green)
                }
                capability("terminal", "Agent", ref.agent, .secondary)
            }
            .padding(12)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))

            Text(message).font(.caption.monospaced()).foregroundStyle(.tertiary).lineLimit(3)

            HStack {
                Button("Cancel", role: .cancel) { session.close() }
                Spacer()
                Button(buttonTitle) {
                    session.approveTrustAndRetry(requireAuth: edited)
                }
                .keyboardShortcut(.defaultAction)
                .buttonStyle(.borderedProminent)
                .tint(openEgress ? .red : .accentColor)
            }
        }
        .padding(20)
        .frame(maxWidth: 460)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.black.opacity(0.25))
    }

    private func capability(_ symbol: String, _ title: String, _ value: String, _ color: Color) -> some View {
        HStack(spacing: 8) {
            Image(systemName: symbol).foregroundStyle(color == .secondary ? .secondary : color).frame(width: 20)
            Text(title).font(.callout.weight(.medium))
            Spacer()
            Text(value).font(.callout).foregroundStyle(color == .secondary ? .secondary : color)
        }
    }
}
