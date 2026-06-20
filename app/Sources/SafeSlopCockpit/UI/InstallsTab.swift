import SwiftUI

/// InstallsTab is the GUI over the engine's pinned, fail-closed installer (InstallPlan/InstallApply
/// RPCs). It shows the desired-state diff (install/upgrade/ok per tool), then streams Apply progress.
/// The engine does the pinning + signature verification; this tab only previews and triggers it.
struct InstallsTab: View {
    @State private var actions: [Safeslop_Control_V1_InstallAction] = []
    @State private var status: String = "loading plan…"
    @State private var applying = false
    @State private var log: [String] = []

    private var needsWork: Bool { actions.contains { $0.kind != "ok" } }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("Tools").font(.title3.weight(.medium)).foregroundStyle(.secondary)
                Spacer()
                Button("Re-plan", systemImage: "arrow.clockwise") { Task { await loadPlan() } }
                    .disabled(applying)
            }
            Text(status).font(.callout).foregroundStyle(.secondary)

            if actions.isEmpty {
                ContentUnavailableView("No plan", systemImage: "shippingbox",
                                       description: Text("The engine reported no install actions."))
                    .frame(maxHeight: .infinity)
            } else {
                List(actions, id: \.name) { a in
                    HStack {
                        Image(systemName: symbol(a.kind)).foregroundStyle(color(a.kind))
                        Text(a.name).font(.headline)
                        Spacer()
                        Text(a.kind == "ok" ? a.current : "\(a.current.isEmpty ? "—" : a.current) → \(a.desired)")
                            .font(.caption.monospaced()).foregroundStyle(.secondary)
                    }
                }
            }

            if !log.isEmpty {
                ScrollView { Text(log.joined(separator: "\n")).font(.caption.monospaced())
                    .frame(maxWidth: .infinity, alignment: .leading) }
                    .frame(height: 90)
                    .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 6))
            }

            HStack {
                Spacer()
                Button(applying ? "Applying…" : "Install / Upgrade") { Task { await apply() } }
                    .buttonStyle(.borderedProminent)
                    .disabled(applying || !needsWork)
            }
        }
        .padding()
        .task { await loadPlan() }
    }

    private func loadPlan() async {
        guard await EngineConnection.ensureServing() else { status = "engine unreachable"; return }
        do {
            actions = try await EngineConnection.installPlan()
            let todo = actions.filter { $0.kind != "ok" }.count
            status = todo == 0 ? "all tools up to date" : "\(todo) to install/upgrade"
        } catch {
            status = "plan failed: \(error)"
        }
    }

    private func apply() async {
        applying = true; log = []
        defer { applying = false }
        do {
            try await EngineConnection.installApply { event in
                await MainActor.run {
                    let tool = event.tool.isEmpty ? "" : "[\(event.tool)] "
                    log.append("\(tool)\(event.msg)")
                }
            }
            await loadPlan()
        } catch {
            log.append("apply failed: \(error)")
        }
    }

    private func symbol(_ kind: String) -> String {
        switch kind {
        case "install": return "arrow.down.circle"
        case "upgrade": return "arrow.up.circle"
        default: return "checkmark.circle"
        }
    }
    private func color(_ kind: String) -> Color {
        switch kind {
        case "install": return .blue
        case "upgrade": return .orange
        default: return .green
        }
    }
}
