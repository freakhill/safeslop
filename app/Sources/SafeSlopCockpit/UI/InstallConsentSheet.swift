import SwiftUI

/// InstallConsentSheet is the proportionate consent gate shown before an install runs (specs/0037).
/// It surfaces the preview the engine computed (tools.InstallPreview, carried on ToolStatus): the
/// verification posture, the precautions safeslop takes, and the exact command. A verified-pin or brew
/// install is a single click; an UNVERIFIED remote-script install (a curl|sh / npm tool with no checksum
/// pin) requires the user to type the tool's name first — friction proportionate to the higher blast
/// radius. The gate only previews; the actual install stays the existing InstallTool stream.
struct InstallConsentSheet: View {
    let tool: Safeslop_Control_V1_ToolStatus
    let onConfirm: () -> Void
    let onCancel: () -> Void
    @State private var typed = ""

    private var unverified: Bool { tool.needsConsent }
    /// Verified/brew arm immediately; an unverified script arms only once the user retypes the tool name.
    private var armed: Bool { !unverified || typed.trimmingCharacters(in: .whitespaces) == tool.name }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            VStack(alignment: .leading, spacing: 3) {
                Text("Install \(tool.name)").font(.title3.weight(.semibold))
                if !tool.note.isEmpty {
                    Text(tool.note).font(.callout).foregroundStyle(.secondary)
                }
            }

            badge
            Text(tool.precautions).font(.callout).fixedSize(horizontal: false, vertical: true)

            VStack(alignment: .leading, spacing: 4) {
                Text("Command").font(.caption.weight(.semibold)).foregroundStyle(.secondary)
                Text(tool.installHint.isEmpty ? "—" : tool.installHint)
                    .font(.caption.monospaced()).textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(8)
                    .background(.quaternary.opacity(0.4), in: RoundedRectangle(cornerRadius: 6))
                if tool.verification == "verified-pin" { provenance }
            }

            if unverified {
                VStack(alignment: .leading, spacing: 4) {
                    Text("This runs a remote script with your privileges. Type “\(tool.name)” to confirm you read the command.")
                        .font(.caption).foregroundStyle(.orange).fixedSize(horizontal: false, vertical: true)
                    TextField("type \(tool.name)", text: $typed)
                        .textFieldStyle(.roundedBorder).frame(maxWidth: 220)
                }
            }

            HStack {
                Button("Cancel", role: .cancel, action: onCancel)
                Spacer()
                Button(unverified ? "Run install" : "Install", action: onConfirm)
                    .buttonStyle(.borderedProminent)
                    .tint(unverified ? .orange : .accentColor)
                    .disabled(!armed)
                    .keyboardShortcut(armed && !unverified ? .defaultAction : nil)
            }
        }
        .padding(20)
        .frame(width: 460)
    }

    @ViewBuilder private var badge: some View {
        switch tool.verification {
        case "verified-pin": pill("sha256-verified pin", "checkmark.seal.fill", .green)
        case "brew": pill("installed via Homebrew", "mug.fill", .blue)
        case "unverified-run": pill("unverified remote script", "exclamationmark.triangle.fill", .orange)
        default: EmptyView()
        }
    }

    private func pill(_ text: String, _ icon: String, _ color: Color) -> some View {
        Label(text, systemImage: icon)
            .font(.caption.weight(.semibold)).foregroundStyle(color)
            .padding(.horizontal, 8).padding(.vertical, 3)
            .background(color.opacity(0.14), in: Capsule())
    }

    @ViewBuilder private var provenance: some View {
        VStack(alignment: .leading, spacing: 2) {
            if !tool.sourceURL.isEmpty {
                Text("source: \(tool.sourceURL)")
                    .font(.caption2).foregroundStyle(.secondary).textSelection(.enabled)
                    .lineLimit(1).truncationMode(.middle)
            }
            HStack(spacing: 10) {
                if !tool.pinnedVersion.isEmpty { Text("version \(tool.pinnedVersion)") }
                if tool.sha256.count >= 12 { Text("sha256 \(tool.sha256.prefix(12))…") }
            }
            .font(.caption2).foregroundStyle(.secondary)
        }
    }
}
