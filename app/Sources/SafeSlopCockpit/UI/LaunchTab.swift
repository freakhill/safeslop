import SwiftUI

/// LaunchTab lists the engine's profiles safest-tier-first and opens a session window per pick. A
/// profile whose config dir went missing is grayed + non-launchable (cleanup nudge). Untrusted/changed
/// profiles still launch — the session window's trust sheet is the gate (specs/0024).
struct LaunchTab: View {
    @Environment(EngineModel.self) private var engine
    @Environment(\.openWindow) private var openWindow

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack {
                Text("practice safe slop")
                    .font(.title3.weight(.medium)).foregroundStyle(.secondary)
                Spacer()
                if let v = engine.version {
                    Text("engine \(v)").font(.caption.monospaced()).foregroundStyle(.secondary)
                }
            }
            Text(engine.status).font(.callout).foregroundStyle(.secondary)

            if engine.profiles.isEmpty {
                if engine.reachable {
                    ContentUnavailableView("No profiles", systemImage: "tray",
                                           description: Text("Add a safeslop.cue with profiles, then Refresh."))
                        .frame(maxHeight: .infinity)
                } else {
                    // Distinguish "engine down" from "no profiles" — the wrong message sends the user to
                    // edit a safeslop.cue when the real fix is the engine reconnecting (ayo #8).
                    ContentUnavailableView("Engine unreachable", systemImage: "bolt.horizontal.circle",
                                           description: Text("Couldn't reach `safeslop serve` — it reconnects automatically when the engine is back."))
                        .frame(maxHeight: .infinity)
                }
            } else {
                if !engine.reachable {
                    // Last-known rows, not a blank grid: say so honestly, with when they were last fresh.
                    Label("Engine unreachable — showing last sync \(engine.lastSyncLabel ?? "—")",
                          systemImage: "bolt.horizontal.circle")
                        .font(.caption.weight(.medium)).foregroundStyle(.orange)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
                List(engine.profiles) { ref in
                    row(ref)
                }
            }

            Button("Refresh", systemImage: "arrow.clockwise") { Task { await engine.refresh() } }
        }
        .padding()
    }

    @ViewBuilder
    private func row(_ ref: ProfileRef) -> some View {
        let missing = ref.configDirMissing
        HStack {
            // Ecusson: color is the chip background; the border WEIGHT (rank) is the non-color danger
            // channel so the chip reads in grayscale / for the colorblind (ayo S2). Glyph = tier.
            RiskBadge(symbol: ref.tierSymbol, color: ref.riskColor, rank: ref.dangerRank).help(ref.tierNote)
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 6) {
                    Text(ref.name).font(.headline)
                    // symbol+word+color triad (macOS TCC / Little Snitch): the WORD carries danger.
                    Text(ref.dangerWord)
                        .font(.caption2.weight(.bold))
                        .padding(.horizontal, 5).padding(.vertical, 1)
                        .background(ref.riskColor.opacity(0.18), in: Capsule())
                        .foregroundStyle(ref.riskColor)
                }
                // Fixed-width columns so the eye scans one axis (agent / tier / network) straight down the
                // list instead of re-parsing a free-flowing "a · b · c" per row (ayo S2). The window is
                // sized to fit these without truncation; a narrow window tail-truncates rather than wraps.
                HStack(spacing: 8) {
                    Text(ref.agent).frame(width: 64, alignment: .leading)
                    Text(ref.tierLabel).frame(width: 124, alignment: .leading)
                    Text("net:\(ref.netLabel)").frame(width: 96, alignment: .leading)
                }
                .font(.caption).foregroundStyle(.secondary).lineLimit(1)
                // Show what's UNRESTRICTED as loudly as the line above shows what's bounded (ayo S2):
                // a fully-contained profile shows no chips — honest, not scary.
                let openAxes = ref.riskAxes.filter { !$0.restricted }
                if !openAxes.isEmpty {
                    HStack(spacing: 4) {
                        ForEach(openAxes) { ax in
                            Text("\(ax.name): \(ax.value)")
                                .font(.caption2.weight(.semibold))
                                .padding(.horizontal, 5).padding(.vertical, 1)
                                .background(ax.color.opacity(0.18), in: Capsule())
                                .foregroundStyle(ax.color)
                        }
                    }
                }
                if !ref.riskHeadline.isEmpty {
                    Text(ref.riskHeadline).font(.caption2.weight(.medium)).foregroundStyle(ref.riskColor)
                }
            }
            if missing {
                badge("missing path", .secondary)
            } else {
                trustControl(ref)
            }
            Spacer()
            Image(systemName: missing ? "exclamationmark.triangle" : "arrow.up.forward.app")
                .foregroundStyle(.secondary)
        }
        // muted until approved; grayed harder when its config dir is gone.
        .opacity(missing ? 0.4 : (!engine.reachable ? 0.5 : (ref.isTrusted ? 1 : 0.6)))
        .contentShape(Rectangle())
        .onTapGesture { if !missing && engine.reachable { openWindow(id: "session", value: ref) } }
        // hover shows the underlying technologies powering this profile (policy.TechStack).
        .help(ref.techStack.isEmpty ? ref.tierNote : ref.techStack.joined(separator: "\n"))
    }

    /// The trailing trust control. When trusted it is a clickable menu whose one action is Revoke (the
    /// symmetric reverse of granting — ayo #3; one click, no biometric, since revoke removes privilege).
    /// When untrusted/changed it is the existing badge. The Menu consumes its own clicks, so tapping it
    /// never falls through to the row's launch gesture.
    @ViewBuilder
    private func trustControl(_ ref: ProfileRef) -> some View {
        if ref.isTrusted {
            Menu {
                Button("Revoke trust", role: .destructive) { Task { await revoke(ref) } }
            } label: {
                Label("trusted", systemImage: "checkmark.shield.fill")
                    .font(.caption2.weight(.semibold)).foregroundStyle(.green)
            }
            .menuStyle(.borderlessButton).fixedSize()
            .help("Trusted — click to revoke")
        } else if let b = ref.trustBadge {
            badge(b.text, b.color)
        }
    }

    /// Revoke this profile's trust, then refresh so the row reflects the new (untrusted) state.
    private func revoke(_ ref: ProfileRef) async {
        do {
            try await EngineConnection.untrust(configPath: ref.configDir)
        } catch {
            // Best-effort: a failed revoke leaves trust intact; the refresh below re-reads ground truth.
        }
        await engine.refresh()
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.semibold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }
}
