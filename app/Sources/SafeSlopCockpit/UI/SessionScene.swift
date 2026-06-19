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

    var body: some View {
        VStack(spacing: 0) {
            header
            TerminalBridge(session: session)
                .frame(minWidth: 480, minHeight: 280)
            footer
        }
        .padding(border)
        .background(ref.trustColor)
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
        case .running: return .green
        case .closed: return .gray
        case .error: return .red
        }
    }

    private var statusText: String {
        switch session.state {
        case .opening: return "opening…"
        case .running: return "running"
        case .closed: return session.exitCode.map { "exited (\($0))" } ?? "closed"
        case .error(let e): return "error: \(e)"
        }
    }
}
