import SwiftUI

/// A Codable handle for a profile, used as the value type for the per-session WindowGroup so a
/// session window can be reopened/restored. Mirrors the engine's Safeslop_Control_V1_Profile.
struct ProfileRef: Codable, Hashable, Identifiable {
    var name: String
    var agent: String
    var environment: String
    var network: String
    var tier: String        // honest isolation tier from the engine (policy.EnvTier) — single source of truth
    var tierNote: String    // the one-line honest caveat
    var trustStatus: String // "trusted" | "untrusted" | "changed" (the engine's trust gate state)
    var configDir: String   // abs dir holding this safeslop.cue; the cockpit runs `safeslop run` here
    var riskHeadline: String // arbiter one-liner consequence (policy.RiskSummary)
    var riskLevel: String    // "high" | "elevated" | "contained" — color only
    var riskLines: [String]  // break-glass consequence sentences
    var techStack: [String]  // underlying technologies (policy.TechStack) — Launch hover tooltip
    var id: String { name }

    init(name: String, agent: String, environment: String, network: String,
         tier: String = "", tierNote: String = "", trustStatus: String = "untrusted", configDir: String = "",
         riskHeadline: String = "", riskLevel: String = "contained", riskLines: [String] = [], techStack: [String] = []) {
        self.name = name; self.agent = agent; self.environment = environment; self.network = network
        self.tier = tier; self.tierNote = tierNote; self.trustStatus = trustStatus; self.configDir = configDir
        self.riskHeadline = riskHeadline; self.riskLevel = riskLevel; self.riskLines = riskLines; self.techStack = techStack
    }
    init(_ p: Safeslop_Control_V1_Profile) {
        self.init(name: p.name, agent: p.agent, environment: p.environment, network: p.network,
                  tier: p.tier, tierNote: p.tierNote, trustStatus: p.trustStatus, configDir: p.configDir,
                  riskHeadline: p.riskHeadline, riskLevel: p.riskLevel, riskLines: p.riskLines, techStack: p.techStack)
    }
    var proto: Safeslop_Control_V1_Profile {
        .with {
            $0.name = name; $0.agent = agent; $0.environment = environment; $0.network = network
            $0.tier = tier; $0.tierNote = tierNote; $0.trustStatus = trustStatus; $0.configDir = configDir
            $0.riskHeadline = riskHeadline; $0.riskLevel = riskLevel; $0.riskLines = riskLines; $0.techStack = techStack
        }
    }

    /// Arbiter color band (research finding #2): high = red, elevated = orange, contained = green.
    var riskColor: Color {
        switch riskLevel {
        case "high": return .red
        case "elevated": return .orange
        default: return .green
        }
    }

    /// Danger level as a WORD — the non-color channel the ecusson's background color must not be the
    /// sole carrier of (ayo S2, headline finding 2). Mirrors `riskLevel`, uppercased for the badge, so
    /// risk reads in grayscale, for the ~8% red-green colorblind, and in a screenshot. Unknown bands
    /// fall back to the safe word, matching `riskColor`'s green default.
    var dangerWord: String {
        switch riskLevel {
        case "high": return "HIGH"
        case "elevated": return "ELEVATED"
        default: return "CONTAINED"
        }
    }

    /// Danger as a SHAPE channel: the ecusson's border weight scales with this rank (high 2 / elevated 1
    /// / contained 0), so the chip alone signals danger with color stripped. Redundant with `riskColor`.
    var dangerRank: Int {
        switch riskLevel {
        case "high": return 2
        case "elevated": return 1
        default: return 0
        }
    }

    var isTrusted: Bool { trustStatus == "trusted" }
    /// A launcher badge for an unapproved policy (nil when trusted).
    var trustBadge: (text: String, color: Color)? {
        switch trustStatus {
        case "changed": return ("changed — review", .red)
        case "trusted": return nil
        default: return ("not trusted", .orange)
        }
    }

    /// Trust color (specs/0014 §5): vm/container = amber; else red for open egress, green for deny.
    var trustColor: Color {
        if environment == "vm" || environment == "container" { return .orange }
        return network == "allow" ? .red : .green
    }

    /// SF Symbol for the tier — presentation only; the honest tier *label* is `tier` (from EnvTier).
    var tierSymbol: String {
        switch environment {
        case "host": return "exclamationmark.octagon.fill"
        case "container": return "scope"
        case "vm": return "externaldrive.connected.to.line.below.fill"
        default: return "lock.shield.fill" // sandbox
        }
    }
    var tierLabel: String { tier.isEmpty ? environment : tier }

    /// Safest-tier-first ordering for the Launch tab + dock menu (research G/H): vm < container <
    /// sandbox < host, so the strongest isolation sorts to the top and host (no isolation) to the
    /// bottom. The safe path is literally the first one you reach.
    var tierRank: Int {
        switch environment {
        case "vm": return 0
        case "container": return 1
        case "sandbox": return 2
        default: return 3 // host
        }
    }

    /// A profile whose config dir no longer exists can't launch; the Launch tab grays it out and
    /// nudges cleanup rather than failing at click time (research E/last-used hygiene).
    var configDirMissing: Bool {
        !configDir.isEmpty && !FileManager.default.fileExists(atPath: configDir)
    }

    /// host tier has no sandbox, so `network` is NOT enforced there — say "unenforced" rather than
    /// claiming "deny" (the agent has full host network). Only sandbox/container/vm enforce it.
    var netEnforced: Bool { environment != "host" }
    var netLabel: String { netEnforced ? network : "unenforced" }

    /// Defense in depth: the profile name must be shell-safe-ish and the config dir absolute — even
    /// though we never invoke a shell (the terminal runs `safeslop` via an argv list, below).
    static let safeName = try! NSRegularExpression(pattern: "^[A-Za-z0-9._-]+$")
    var runnable: Bool {
        configDir.hasPrefix("/") &&
            ProfileRef.safeName.firstMatch(in: name, range: NSRange(name.startIndex..., in: name)) != nil
    }
    /// The session terminal runs `safeslop run <profile>` DIRECTLY — no shell, an argv list, with cwd
    /// set to the policy dir via posix_spawn. /usr/bin/env finds safeslop on PATH; each arg is a
    /// literal, so a hostile profile name / path can't inject host commands (the trust gate + run
    /// happen as the user, before the sandbox). `safeslop run` itself does the gate + isolation + ctty.
    var runExecutable: String { EngineBinary.resolved.executable }
    var runArgs: [String] { EngineBinary.resolved.prefixArgs + ["run", name] }
    var runCwd: String { configDir }
}

/// SessionHostView drives one session window: gate trust (and Touch ID for the privilege boundary),
/// then host a native local terminal running `safeslop run`. The window closes when it exits.
struct SessionHostView: View {
    let ref: ProfileRef
    @Environment(\.dismissWindow) private var dismissWindow
    @State private var phase: Phase

    enum Phase: Equatable { case preparing, trust(changed: Bool), hostConsent(Preflight), running, denied(String) }

    init(ref: ProfileRef) {
        self.ref = ref
        _phase = State(initialValue: .preparing)
    }

    var body: some View {
        CockpitChrome(ref: ref) {
            switch phase {
            case .preparing:
                ProgressView().controlSize(.small)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            case .trust(let changed):
                TrustSheet(ref: ref, changed: changed,
                           onApprove: { Task { await approve(changed: changed) } },
                           onCancel: { dismissWindow(value: ref) })
            case .hostConsent(let pf):
                HostConsentView(ref: ref, preflight: pf,
                                onLaunch: { phase = .running },
                                onCancel: { dismissWindow(value: ref) })
            case .running:
                LocalTerminal(executable: ref.runExecutable, args: ref.runArgs, currentDirectory: ref.runCwd,
                              onExit: { _ in dismissWindow(value: ref) })
            case .denied(let why):
                ContentUnavailableView("Cannot start", systemImage: "xmark.octagon",
                                       description: Text(why))
            }
        }
        .navigationTitle("\(ref.name) — SafeSlop")
        .task { await prepare() }
    }

    private func prepare() async {
        if ref.trustStatus != "trusted" {
            phase = .trust(changed: ref.trustStatus == "changed")
            return // wait for the trust sheet
        }
        await afterTrust()
    }

    private func approve(changed: Bool) async {
        if changed { // re-approving an *edited* policy is a privilege boundary -> Touch ID
            let ok = await BiometricGate.confirm(reason: "re-approve the edited safeslop.cue for “\(ref.name)”.")
            if !ok { return } // stay on the sheet
        }
        do {
            _ = try await EngineConnection.trust(configPath: ref.configDir)
        } catch {
            phase = .denied("trust failed: \(String(describing: error))")
            return
        }
        await afterTrust()
    }

    private func afterTrust() async {
        guard ref.runnable else {
            phase = .denied("refusing to launch: profile name or config path is not valid"); return
        }
        if ref.environment == "host" {
            // Host tier has no isolation: gate on a per-launch comprehension act (specs/0030), not a
            // bare Touch ID. Fetch the engine-authored statements, then swap to the in-window consent
            // phase; the view runs BiometricGate only once the user has matched ground truth.
            do {
                let pf = try await EngineConnection.preflightHostLaunch(profile: ref.name, configPath: ref.configDir)
                phase = .hostConsent(Preflight(pf))
            } catch {
                phase = .denied("could not prepare the host-launch confirmation: \(String(describing: error))")
            }
            return
        }
        phase = .running
    }
}

/// CockpitChrome = the decorated frame around the session content: an ambient host-drawn trust tint
/// (un-spoofable — the agent owns only the terminal buffer) plus a header (identity/tier) and footer.
struct CockpitChrome<Content: View>: View {
    let ref: ProfileRef
    @ViewBuilder var content: Content
    private let border: CGFloat = 3
    private let barTint: Double = 0.18
    private let ambientTint: Double = 0.05

    var body: some View {
        VStack(spacing: 0) {
            header
            content
                .frame(minWidth: 480, minHeight: 280)
                .overlay(ref.trustColor.opacity(ambientTint).allowsHitTesting(false))
            footer
        }
        .padding(border)
        .background(ref.trustColor)
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text(ref.name).font(.headline)
            Label(ref.tierLabel, systemImage: ref.tierSymbol)
                .font(.caption.weight(.semibold))
                .foregroundStyle(ref.trustColor)
                .help(ref.tierNote)
            badge("net: \(ref.netLabel)", ref.netEnforced ? (ref.network == "allow" ? .red : .green) : .secondary)
            Spacer()
            Text("agent: \(ref.agent)").font(.caption).foregroundStyle(.secondary)
        }
        .padding(.horizontal, 10).padding(.vertical, 6)
        .background(ref.trustColor.opacity(barTint))
        .background(.bar)
    }

    private var footer: some View {
        HStack {
            Image(systemName: ref.tierSymbol).font(.caption2).foregroundStyle(ref.trustColor)
            Text(ref.configDir).font(.caption.monospaced()).foregroundStyle(.tertiary)
                .lineLimit(1).truncationMode(.head)
            Spacer()
        }
        .padding(.horizontal, 10).padding(.vertical, 4)
        .background(ref.trustColor.opacity(barTint))
        .background(.bar)
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.semibold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }
}

/// TrustSheet covers the session in place when the policy isn't approved. It states the profile's
/// capabilities in plain language (not CUE), names the highest-risk one in the approve button, and
/// (for an edited policy) gates approval behind Touch ID via onApprove.
struct TrustSheet: View {
    let ref: ProfileRef
    let changed: Bool
    let onApprove: () -> Void
    let onCancel: () -> Void

    private var openEgress: Bool { ref.network == "allow" }
    private var buttonTitle: String {
        if changed { return "Re-trust edited policy (Touch ID)" }
        return openEgress ? "Trust & Launch — allows open network" : "Trust & Launch"
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            Label(changed ? "This policy changed — review & re-trust" : "Review & trust this profile",
                  systemImage: "lock.shield")
                .font(.title3.weight(.semibold))
            Text("safeslop won't run this repo's policy until you approve it. Review what “\(ref.name)” can do, then trust it.")
                .font(.callout).foregroundStyle(.secondary)

            VStack(alignment: .leading, spacing: 8) {
                capability(ref.tierSymbol, "Isolation", ref.tierLabel, ref.trustColor)
                if ref.netEnforced {
                    if openEgress {
                        capability("network", "Network", "open — the agent can reach the internet", .red)
                    } else {
                        capability("network.slash", "Network", "denied — offline", .green)
                    }
                } else {
                    capability("network", "Network", "unenforced — host tier has no boundary", .red)
                }
                capability("terminal", "Agent", ref.agent, .secondary)
            }
            .padding(12)
            .background(.quaternary.opacity(0.5), in: RoundedRectangle(cornerRadius: 8))

            // The arbiter's break-glass consequences — what the agent can do if compromised.
            ArbiterPane(ref: ref)
                .padding(12)
                .background(ref.riskColor.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))

            Text(ref.configDir).font(.caption.monospaced()).foregroundStyle(.tertiary).lineLimit(1).truncationMode(.head)

            HStack {
                Button("Cancel", role: .cancel, action: onCancel)
                Spacer()
                Button(buttonTitle, action: onApprove)
                    .keyboardShortcut(.defaultAction)
                    .buttonStyle(.borderedProminent)
                    .tint(openEgress || changed ? .red : .accentColor)
            }
        }
        .padding(20)
        .frame(maxWidth: 460)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .shadow(radius: 20)
        .padding(40)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.black.opacity(0.25))
    }

    private func capability(_ symbol: String, _ title: String, _ value: String, _ color: Color) -> some View {
        HStack(spacing: 8) {
            Image(systemName: symbol).foregroundStyle(color == .secondary ? .secondary : color).frame(width: 20)
            Text(title).font(.callout.weight(.medium))
            Spacer()
            Text(value).font(.callout).foregroundStyle(color == .secondary ? .secondary : color)
        }
    }
}
