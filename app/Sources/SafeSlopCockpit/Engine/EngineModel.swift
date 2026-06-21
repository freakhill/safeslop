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

    /// When the last SUCCESSFUL profile list landed — stamped only on success, so it survives a later
    /// unreachable refresh and the Launch tab can show "last sync HH:MM" over the last-known rows
    /// instead of an empty grid (ayo #8). nil until the first successful sync.
    var lastSync: Date?

    /// The control-plane client. Defaults to the live UDS gRPC; tests inject a fake EngineClient.
    @ObservationIgnored private let engine: EngineClient
    init(engine: EngineClient = LiveEngineClient()) { self.engine = engine }

    /// Ensure the engine is up, then refresh version + profiles. Idempotent (ping-first).
    func refresh() async {
        status = "ensuring safeslop serve…"
        guard await engine.ensureServing() else {
            reachable = false
            status = "could not reach or start `safeslop serve` (is safeslop on PATH?)"
            return
        }
        reachable = true
        version = await engine.ping()
        do {
            let list = try await engine.listProfiles(configPath: "")
            // safest tier first (vm→container→sandbox→host), then by name (research G/H).
            let refs = list.map(ProfileRef.init)
            profiles = refs.sorted { a, b in
                if a.tierRank != b.tierRank { return a.tierRank < b.tierRank }
                return a.name < b.name
            }
            lastSync = Date()
            status = profiles.isEmpty ? "connected — no profiles found" : "connected"
        } catch {
            status = "listProfiles failed: \(error)"
        }
    }

    /// The last successful sync as a short local time (e.g. "2:31 PM"), or nil if never synced.
    var lastSyncLabel: String? {
        lastSync?.formatted(date: .omitted, time: .shortened)
    }
}
