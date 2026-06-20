import SwiftUI

/// EngineModel is the cockpit's shared connection state: it ensures `safeslop serve` is up, holds the
/// profile list (sorted safest-tier-first), and the engine version. Tabs read it from the SwiftUI
/// environment so they don't each independently start the engine or hold a divergent profile list.
@Observable
@MainActor
final class EngineModel {
    var version: String?
    var status: String = "connecting…"
    var profiles: [ProfileRef] = []
    var reachable: Bool = false

    /// Ensure the engine is up, then refresh version + profiles. Idempotent (ping-first).
    func refresh() async {
        status = "ensuring safeslop serve…"
        guard await EngineConnection.ensureServing() else {
            reachable = false
            status = "could not reach or start `safeslop serve` (is safeslop on PATH?)"
            return
        }
        reachable = true
        version = await EngineConnection.ping()
        do {
            let list = try await EngineConnection.listProfiles()
            // safest tier first (vm→container→sandbox→host), then by name (research G/H).
            let refs = list.map(ProfileRef.init)
            profiles = refs.sorted { a, b in
                if a.tierRank != b.tierRank { return a.tierRank < b.tierRank }
                return a.name < b.name
            }
            status = profiles.isEmpty ? "connected — no profiles found" : "connected"
        } catch {
            status = "listProfiles failed: \(error)"
        }
    }
}
