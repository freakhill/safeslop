import SwiftUI

/// InstallsTab manages the dev tools, runtimes, container/VM hosts, secret managers, and agents
/// safeslop works with (internal/engine/tools). It DETECTS what's already present and how it was
/// installed (brew / cask / standalone), and only ever offers to install a MISSING tool — a present
/// tool just shows its source, so safeslop never clobbers an existing install. People install one
/// tool at a time; there is no install-everything button (specs/0029, the user's requirement).
struct InstallsTab: View {
    @State private var tools: [Safeslop_Control_V1_ToolStatus] = []
    @State private var status = "detecting tools…"
    @State private var installing: Set<String> = []
    @State private var log: [String] = []
    @State private var activeTool: String?
    /// Whether the initial two-phase load has completed. `.task` re-fires every time this tab re-appears
    /// (TabView disappears/reappears tab content); without this gate, each re-entry re-ran the catalog
    /// phase, which resets every row's source to "unknown" — flashing the whole list to gray "?" and
    /// back (the giant flicker). Re-detect remains the explicit manual refresh.
    @State private var didInitialLoad = false

    /// catalog order preserved, grouped into (category, tools) sections.
    private var sections: [(String, [Safeslop_Control_V1_ToolStatus])] {
        var order: [String] = []
        var byCat: [String: [Safeslop_Control_V1_ToolStatus]] = [:]
        for t in tools {
            if byCat[t.category] == nil { order.append(t.category) }
            byCat[t.category, default: []].append(t)
        }
        return order.map { ($0, byCat[$0] ?? []) }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Tools").font(.title3.weight(.medium)).foregroundStyle(.secondary)
                Spacer()
                Text(status).font(.caption).foregroundStyle(.secondary)
                Button("Re-detect", systemImage: "arrow.clockwise") { Task { await load(catalogOnly: false) } }
                    .disabled(!installing.isEmpty)
            }

            List {
                ForEach(sections, id: \.0) { (category, items) in
                    Section(category) {
                        ForEach(items, id: \.name) { tool in row(tool) }
                    }
                }
            }

            if let active = activeTool {
                VStack(alignment: .leading, spacing: 2) {
                    Text("installing \(active)…").font(.caption.weight(.semibold))
                    ScrollView {
                        Text(log.joined(separator: "\n"))
                            .font(.caption.monospaced())
                            .frame(maxWidth: .infinity, alignment: .leading).textSelection(.enabled)
                    }
                    .frame(height: 110)
                    .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 6))
                }
            }
        }
        .padding()
        // instant first paint (every tool with a "?"), then the real brew-backed detection — ONCE.
        // Re-entering the tab must not re-flash the populated rows back to "?" (the giant flicker); the
        // catalog phase only runs while nothing is shown yet, and the whole sequence runs a single time.
        .task {
            guard !didInitialLoad else { return }
            if tools.isEmpty { await load(catalogOnly: true) }
            await load(catalogOnly: false)
            didInitialLoad = true
        }
    }

    @ViewBuilder
    private func row(_ t: Safeslop_Control_V1_ToolStatus) -> some View {
        let unknown = t.source == "unknown"
        HStack(spacing: 10) {
            Image(systemName: unknown ? "questionmark.circle" : (t.present ? "checkmark.circle.fill" : "circle.dashed"))
                .foregroundStyle(unknown ? Color.gray : (t.present ? .green : .gray))
            VStack(alignment: .leading, spacing: 1) {
                Text(t.name).font(.headline)
                Text(t.note).font(.caption).foregroundStyle(.secondary)
            }
            Spacer()
            if unknown {
                Text("?").font(.callout.weight(.bold)).foregroundStyle(.secondary).help("detecting…")
            } else if t.present {
                if !t.shadowedPaths.isEmpty {
                    // The resolved binary shadows others of the same name later on PATH — picking the
                    // wrong one can silently differ from what the user expects (ayo #6 / specs/0035).
                    Label("shadows \(t.shadowedPaths.count)", systemImage: "exclamationmark.triangle.fill")
                        .font(.caption2.weight(.semibold)).foregroundStyle(.orange)
                        .help("Resolves to \(t.path)\nAlso on PATH (shadowed): \(t.shadowedPaths.joined(separator: ", "))")
                }
                Text(t.source).font(.caption2.weight(.semibold))
                    .padding(.horizontal, 6).padding(.vertical, 2)
                    .background(.green.opacity(0.15), in: Capsule()).foregroundStyle(.green)
                    .help(t.path)
            } else if t.installable {
                Button {
                    Task { await install(t.name) }
                } label: {
                    if installing.contains(t.name) {
                        ProgressView().controlSize(.small)
                    } else {
                        Text("Install")
                    }
                }
                .buttonStyle(.bordered)
                .disabled(!installing.isEmpty)
                .help(t.installHint.isEmpty ? "install \(t.name)" : t.installHint)
            } else {
                Text("manual").font(.caption2).foregroundStyle(.tertiary)
                    .help("no automatic install route — install \(t.name) yourself")
            }
        }
        .padding(.vertical, 2)
        .opacity(unknown ? 0.6 : 1)
    }

    private func load(catalogOnly: Bool) async {
        guard await EngineConnection.ensureServing() else { status = "engine unreachable"; return }
        do {
            tools = try await EngineConnection.listTools(catalogOnly: catalogOnly)
            if catalogOnly {
                status = "detecting…"
            } else {
                let missing = tools.filter { $0.installable }.count
                let present = tools.filter { $0.present }.count
                status = "\(present) installed · \(missing) available"
            }
        } catch {
            status = "detect failed: \(error)"
        }
    }

    private func install(_ name: String) async {
        installing.insert(name); activeTool = name; log = []
        defer { installing.remove(name) }
        do {
            try await EngineConnection.installTool(name: name) { line in
                await MainActor.run { log.append(line) }
            }
            log.append("— done —")
            await load(catalogOnly: false)
        } catch {
            log.append("install failed: \(error)")
        }
    }
}
