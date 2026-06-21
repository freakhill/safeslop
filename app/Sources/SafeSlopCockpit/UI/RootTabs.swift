import SwiftUI

/// RootTabs is the cockpit's main window: three tabs — Installs / Launch / Create — over the shared
/// EngineModel (specs/0029). The small "practice safe slop" identity stays in the toolbar so the
/// trust language is consistent across tabs (research I/host-drawn chrome). Session windows are still
/// separate WindowGroups opened from the Launch tab.
struct RootTabs: View {
    @State private var engine = EngineModel()
    @State private var tab = RootTabs.initialTab

    var body: some View {
        TabView(selection: $tab) {
            LaunchTab()
                .tabItem { Label("Launch", systemImage: "play.circle") }
                .tag("launch")
            InstallsTab()
                .tabItem { Label("Installs", systemImage: "shippingbox") }
                .tag("installs")
            CreateTab()
                .tabItem { Label("Create", systemImage: "plus.square.on.square") }
                .tag("create")
        }
        .environment(engine)
        .frame(minWidth: 460, minHeight: 480)
        .task { await engine.refresh() }
    }

    /// The tab shown at launch — Launch by default. The screenshot harness sets COCKPIT_TAB so it can
    /// capture Installs/Create without a click (the GUI self-test instrumentation).
    static var initialTab: String {
        let t = ProcessInfo.processInfo.environment["COCKPIT_TAB"]?.lowercased() ?? "launch"
        return ["launch", "installs", "create"].contains(t) ? t : "launch"
    }
}
