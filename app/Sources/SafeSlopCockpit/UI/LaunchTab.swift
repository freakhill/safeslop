import SwiftUI

/// LaunchTab lists the engine's profiles safest-tier-first and opens a session window per pick. A
/// profile whose config dir went missing is grayed + non-launchable (cleanup nudge). Untrusted/changed
/// profiles still launch — the session window's trust sheet is the gate (specs/0024).
struct LaunchTab: View {
    @Environment(EngineModel.self) private var engine
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("practice safe slop")
                    .font(.title3.weight(.medium)).foregroundStyle(.secondary)
                Spacer()
                if let v = engine.version {
                    Text("engine \(v)").font(.caption.monospaced()).foregroundStyle(.secondary)
                }
            }
            Text(engine.status).font(.callout).foregroundStyle(.secondary)

            if engine.profiles.isEmpty {
                ContentUnavailableView("No profiles", systemImage: "tray",
                                       description: Text("Add a safeslop.cue with profiles, then Refresh."))
                    .frame(maxHeight: .infinity)
            } else {
                List(engine.profiles) { ref in
                    row(ref)
                }
            }

            Button("Refresh", systemImage: "arrow.clockwise") { Task { await engine.refresh() } }
        }
        .padding()
    }

    @ViewBuilder
    private func row(_ ref: ProfileRef) -> some View {
        let missing = ref.configDirMissing
        Button {
            if !missing { openWindow(id: "session", value: ref) }
        } label: {
            HStack {
                Image(systemName: ref.tierSymbol).foregroundStyle(ref.trustColor).help(ref.tierNote)
                VStack(alignment: .leading) {
                    Text(ref.name).font(.headline)
                    Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                        .font(.caption).foregroundStyle(.secondary)
                }
                if missing {
                    badge("missing path", .secondary)
                } else if let b = ref.trustBadge {
                    badge(b.text, b.color)
                }
                Spacer()
                Image(systemName: missing ? "exclamationmark.triangle" : "arrow.up.forward.app")
            }
            // muted until approved; grayed harder when its config dir is gone.
            .opacity(missing ? 0.4 : (ref.isTrusted ? 1 : 0.6))
        }
        .buttonStyle(.plain)
        .disabled(missing)
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.semibold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }
}
