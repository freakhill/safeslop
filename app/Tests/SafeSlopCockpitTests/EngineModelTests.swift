import Testing
@testable import SafeSlopCockpit

/// FakeEngineClient feeds EngineModel canned responses so its state machine is tested with no live
/// `safeslop serve`. Each closure is overridable per test.
@MainActor
final class FakeEngineClient: EngineClient {
    var serving = true
    var versionValue: String? = "vFAKE"
    var profilesResult: Result<[Safeslop_Control_V1_Profile], any Error> = .success([])

    func ensureServing() async -> Bool { serving }
    func ping() async -> String? { versionValue }
    func listProfiles(configPath: String) async throws -> [Safeslop_Control_V1_Profile] {
        try profilesResult.get()
    }
}

private func profile(_ name: String, env: String) -> Safeslop_Control_V1_Profile {
    .with {
        $0.name = name
        $0.agent = "claude"
        $0.environment = env
        $0.network = "deny"
        $0.tier = env == "host" ? "no-isolation" : "mistake-guard"
    }
}

struct EngineModelTests {
    @Test @MainActor
    func refreshUnreachableEngineSetsStatus() async {
        let fake = FakeEngineClient()
        fake.serving = false
        let model = EngineModel(engine: fake)

        await model.refresh()

        #expect(model.reachable == false)
        #expect(model.profiles.isEmpty)
        #expect(model.status.contains("could not reach"))
    }

    @Test @MainActor
    func refreshSortsProfilesSafestTierFirst() async {
        let fake = FakeEngineClient()
        // Deliberately out of order: host, sandbox, vm, container — must come back vm→container→sandbox→host.
        fake.profilesResult = .success([
            profile("h", env: "host"),
            profile("s", env: "sandbox"),
            profile("v", env: "vm"),
            profile("c", env: "container"),
        ])
        let model = EngineModel(engine: fake)

        await model.refresh()

        #expect(model.reachable == true)
        #expect(model.version == "vFAKE")
        #expect(model.status == "connected")
        #expect(model.profiles.map(\.environment) == ["vm", "container", "sandbox", "host"])
    }

    @Test @MainActor
    func refreshSameTierSortsByName() async {
        let fake = FakeEngineClient()
        fake.profilesResult = .success([
            profile("zeta", env: "sandbox"),
            profile("alpha", env: "sandbox"),
        ])
        let model = EngineModel(engine: fake)

        await model.refresh()

        #expect(model.profiles.map(\.name) == ["alpha", "zeta"])
    }

    @Test @MainActor
    func refreshEmptyProfilesReportsNoneFound() async {
        let fake = FakeEngineClient()
        fake.profilesResult = .success([])
        let model = EngineModel(engine: fake)

        await model.refresh()

        #expect(model.reachable == true)
        #expect(model.status == "connected — no profiles found")
    }

    @Test @MainActor
    func refreshListErrorSurfacesInStatus() async {
        struct Boom: Error {}
        let fake = FakeEngineClient()
        fake.profilesResult = .failure(Boom())
        let model = EngineModel(engine: fake)

        await model.refresh()

        #expect(model.reachable == true) // engine WAS reachable; the list call is what failed
        #expect(model.status.contains("listProfiles failed"))
    }
}
