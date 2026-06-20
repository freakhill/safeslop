import SwiftUI

/// RootTabs is the cockpit's main window: three tabs — Installs / Launch / Create — over the shared
/// EngineModel (specs/0029). The small "practice safe slop" identity stays in the toolbar so the
/// trust language is consistent across tabs (research I/host-drawn chrome). Session windows are still
/// separate WindowGroups opened from the Launch tab.
struct RootTabs: View {
    @State private var engine = EngineModel()

    var body: some View {
        TabView {
            LaunchTab()
                .tabItem { Label("Launch", systemImage: "play.circle") }
            InstallsTab()
                .tabItem { Label("Installs", systemImage: "shippingbox") }
            CreateTab()
                .tabItem { Label("Create", systemImage: "plus.square.on.square") }
        }
        .environment(engine)
        .frame(minWidth: 460, minHeight: 480)
        .task { await engine.refresh() }
    }
}
