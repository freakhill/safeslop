import SwiftUI
import AppKit

// SafeSlop cockpit entry point. The main window is the three-tab cockpit (Installs / Launch / Create,
// specs/0029); opening a profile from the Launch tab spawns a per-session window (one window == one
// session, specs/0014 §4).
@main
struct SafeSlopCockpitApp: App {
    @NSApplicationDelegateAdaptor(AppDelegate.self) private var appDelegate

    var body: some Scene {
        WindowGroup("SafeSlop", id: "launcher") {
            RootTabs()
        }
        .defaultSize(width: 480, height: 520)

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
