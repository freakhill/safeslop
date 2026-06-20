import SwiftUI
import AppKit

// SafeSlop cockpit entry point. A launcher window lists the engine's profiles; opening one spawns
// a per-session window (specs/0014 §4 — WindowGroup, one window == one session).
@main
struct SafeSlopCockpitApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

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

/// Launched as a bare `swift run` executable (no .app bundle), the app would otherwise come up unable
/// to become the key/active app — its windows take mouse clicks but never keyboard focus, so the
/// embedded terminal can't receive keystrokes. Force a regular foreground app on launch.
final class AppDelegate: NSObject, NSApplicationDelegate {
    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.regular)
        NSApp.activate(ignoringOtherApps: true)
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
                Text("practice safe slop")
                    .font(.title3.weight(.medium))
                    .foregroundStyle(.secondary)
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
                            Image(systemName: ref.tierSymbol)
                                .foregroundStyle(ref.trustColor)
                                .help(ref.tierNote)
                            VStack(alignment: .leading) {
                                Text(ref.name).font(.headline)
                                Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                                    .font(.caption).foregroundStyle(.secondary)
                            }
                            if let badge = ref.trustBadge {
                                Text(badge.text)
                                    .font(.caption2.weight(.semibold))
                                    .padding(.horizontal, 6).padding(.vertical, 2)
                                    .background(badge.color.opacity(0.18), in: Capsule())
                                    .foregroundStyle(badge.color)
                            }
                            Spacer()
                            Image(systemName: "arrow.up.forward.app")
                        }
                        // Listed-but-muted until approved; the row still launches (→ trust sheet).
                        .opacity(ref.isTrusted ? 1 : 0.6)
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
