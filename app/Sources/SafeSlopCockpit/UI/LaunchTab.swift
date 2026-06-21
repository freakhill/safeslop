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
                ContentUnavailableView("No profiles", systemImage: "tray",
                                       description: Text("Add a safeslop.cue with profiles, then Refresh."))
                    .frame(maxHeight: .infinity)
            } else {
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
        Button {
            if !missing { openWindow(id: "session", value: ref) }
        } label: {
            HStack {
                // Ecusson: color is the chip background; the border WEIGHT (rank) is the non-color danger
                // channel so the chip reads in grayscale / for the colorblind (ayo S2). Glyph = tier.
                RiskBadge(symbol: ref.tierSymbol, color: ref.riskColor, rank: ref.dangerRank).help(ref.tierNote)
                VStack(alignment: .leading, spacing: 1) {
                    HStack(spacing: 6) {
                        Text(ref.name).font(.headline)
                        // symbol+word+color triad (macOS TCC / Little Snitch): the WORD carries danger,
                        // not the color alone.
                        Text(ref.dangerWord)
                            .font(.caption2.weight(.bold))
                            .padding(.horizontal, 5).padding(.vertical, 1)
                            .background(ref.riskColor.opacity(0.18), in: Capsule())
                            .foregroundStyle(ref.riskColor)
                    }
                    Text("\(ref.agent) · \(ref.tierLabel) · net:\(ref.netLabel)")
                        .font(.caption).foregroundStyle(.secondary)
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
                } else if let b = ref.trustBadge {
                    badge(b.text, b.color)
                }
                Spacer()
                Image(systemName: missing ? "exclamationmark.triangle" : "arrow.up.forward.app")
            }
            // muted until approved; grayed harder when its config dir is gone.
            .opacity(missing ? 0.4 : (ref.isTrusted ? 1 : 0.6))
        }
        .buttonStyle(.plain)
        .disabled(missing)
        // hover shows the underlying technologies powering this profile (policy.TechStack).
        .help(ref.techStack.isEmpty ? ref.tierNote : ref.techStack.joined(separator: "\n"))
    }

    private func badge(_ text: String, _ color: Color) -> some View {
        Text(text).font(.caption2.weight(.semibold))
            .padding(.horizontal, 6).padding(.vertical, 2)
            .background(color.opacity(0.18), in: Capsule())
            .foregroundStyle(color)
    }
}
