import SwiftUI
import Testing
@testable import SafeSlopCockpit

/// ProfileRef is the value type the Launch/Create views render. Its derived properties (tier order,
/// risk→color, trust/network honesty) are pure and drive what the user sees — assert them headlessly.
struct ProfileRefTests {
    @Test
    func tierRankOrdersStrongestIsolationFirst() {
        // vm < container < sandbox < host: the safe path is the first row you reach.
        #expect(ref(env: "vm").tierRank < ref(env: "container").tierRank)
        #expect(ref(env: "container").tierRank < ref(env: "sandbox").tierRank)
        #expect(ref(env: "sandbox").tierRank < ref(env: "host").tierRank)
    }

    @Test
    func riskColorMatchesArbiterBand() {
        #expect(ref(risk: "high").riskColor == .red)
        #expect(ref(risk: "elevated").riskColor == .orange)
        #expect(ref(risk: "contained").riskColor == .green)
        #expect(ref(risk: "anything-else").riskColor == .green) // default band is contained/green
    }

    @Test
    func dangerWordIsRedundantWithColorBand() {
        // The word carries danger level without color — survives grayscale / colorblindness / screenshots.
        #expect(ref(risk: "high").dangerWord == "HIGH")
        #expect(ref(risk: "elevated").dangerWord == "ELEVATED")
        #expect(ref(risk: "contained").dangerWord == "CONTAINED")
        #expect(ref(risk: "anything-else").dangerWord == "CONTAINED") // default band is contained
    }

    @Test
    func dangerRankOrdersBySeverity() {
        // The rank drives the ecusson border weight — a second, shape-based danger channel.
        #expect(ref(risk: "high").dangerRank == 2)
        #expect(ref(risk: "elevated").dangerRank == 1)
        #expect(ref(risk: "contained").dangerRank == 0)
        #expect(ref(risk: "high").dangerRank > ref(risk: "elevated").dangerRank)
        #expect(ref(risk: "elevated").dangerRank > ref(risk: "contained").dangerRank)
    }

    @Test
    func networkHonestyOnHostTier() {
        // host has no sandbox, so a declared network is NOT enforced — the UI must say so, not "deny".
        let host = ProfileRef(name: "h", agent: "claude", environment: "host", network: "deny")
        #expect(host.netEnforced == false)
        #expect(host.netLabel == "unenforced")

        let sandbox = ProfileRef(name: "s", agent: "claude", environment: "sandbox", network: "deny")
        #expect(sandbox.netEnforced == true)
        #expect(sandbox.netLabel == "deny")
    }

    @Test
    func trustBadgeReflectsTrustStatus() {
        #expect(ref(trust: "trusted").trustBadge == nil)
        #expect(ref(trust: "untrusted").trustBadge?.text == "not trusted")
        #expect(ref(trust: "changed").trustBadge?.text == "changed — review")
        #expect(ref(trust: "trusted").isTrusted)
    }

    @Test
    func protoRoundTripPreservesFields() {
        let original = ProfileRef(name: "dev", agent: "claude", environment: "container",
                                  network: "allow", tier: "blast-box", riskLevel: "elevated")
        let restored = ProfileRef(original.proto)
        #expect(restored == original)
    }

    // helpers
    private func ref(env: String = "sandbox", risk: String = "contained", trust: String = "untrusted") -> ProfileRef {
        ProfileRef(name: "p", agent: "claude", environment: env, network: "deny",
                   trustStatus: trust, riskLevel: risk)
    }
}
