import SwiftUI

/// ArbiterPane renders a profile's safety as concrete break-glass CONSEQUENCES — never a score or a
/// bare tier name (specs/0029, the second load-bearing research finding). The lines come from the
/// engine's policy.RiskSummary, so the editor, the Launch tab, and the trust sheet all show the same
/// capability vocabulary. The header frames it as "if compromised, this profile can…".
struct ArbiterPane: View {
    let ref: ProfileRef
    var compact: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: compact ? 4 : 8) {
            HStack(spacing: 8) {
                Image(systemName: icon).foregroundStyle(ref.riskColor)
                Text(ref.riskHeadline.isEmpty ? ref.tierLabel : ref.riskHeadline)
                    .font(compact ? .caption.weight(.semibold) : .callout.weight(.semibold))
                    .foregroundStyle(ref.riskColor)
            }
            if !compact {
                Text("If this agent is compromised, it can:")
                    .font(.caption).foregroundStyle(.secondary)
                ForEach(ref.riskLines, id: \.self) { line in
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("•").foregroundStyle(.tertiary)
                        Text(line).font(.caption).foregroundStyle(.primary)
                    }
                }
            }
        }
    }

    private var icon: String {
        switch ref.riskLevel {
        case "high": return "exclamationmark.octagon.fill"
        case "elevated": return "exclamationmark.triangle.fill"
        default: return "checkmark.shield.fill"
        }
    }
}
