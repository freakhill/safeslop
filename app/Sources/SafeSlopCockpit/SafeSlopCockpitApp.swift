import SwiftUI

// SafeSlop cockpit entry point. A launcher window lists the engine's profiles; opening one spawns
// a per-session window (specs/0014 §4 — WindowGroup, one window == one session).
@main
struct SafeSlopCockpitApp: App {
    var body: some Scene {
        WindowGroup("SafeSlop", id: "launcher") {
            LauncherView()
        }
        .defaultSize(width: 420, height: 460)

        WindowGroup(id: "session", for: ProfileRef.self) { $ref in
            if let ref { SessionHostView(ref: ref) }
        }
        .defaultSize(width: 820, height: 520)
    }
}

/// LauncherView ensures `safeslop serve` is up, lists profiles, and opens a session window per pick.
struct LauncherView: View {
    @Environment(\.openWindow) private var openWindow
    @State private var version: String?
    @State private var profiles: [ProfileRef] = []
    @State private var status: String = "connecting…"

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("SafeSlop").font(.largeTitle.bold())
                Spacer()
                if let version { Text("engine \(version)").font(.caption.monospaced()).foregroundStyle(.secondary) }
            }
            Text(status).font(.callout).foregroundStyle(.secondary)

            if profiles.isEmpty {
                ContentUnavailableView("No profiles", systemImage: "tray",
                                       description: Text("Add a safeslop.cue with profiles, then Refresh."))
                    .frame(maxHeight: .infinity)
            } else {
                List(profiles) { ref in
                    Button { openWindow(id: "session", value: ref) } label: {
                        HStack {
                            Circle().fill(ref.trustColor).frame(width: 10, height: 10)
                            VStack(alignment: .leading) {
                                Text(ref.name).font(.headline)
                                Text("\(ref.agent) · \(ref.environment) · net:\(ref.network)")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            Spacer()
                            Image(systemName: "arrow.up.forward.app")
                        }
                    }
                    .buttonStyle(.plain)
                }
            }

            Button("Refresh", systemImage: "arrow.clockwise") { Task { await refresh() } }
        }
        .padding()
        .task { await refresh() }
    }

    private func refresh() async {
        status = "ensuring safeslop serve…"
        guard await EngineConnection.ensureServing() else {
            status = "could not reach or start `safeslop serve` (is safeslop on PATH?)"
            return
        }
        version = await EngineConnection.ping()
        do {
            let list = try await EngineConnection.listProfiles()
            profiles = list.map(ProfileRef.init)
            status = profiles.isEmpty ? "connected — no profiles found" : "connected"
        } catch {
            status = "listProfiles failed: \(error)"
        }
    }
}
